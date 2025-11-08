import { useState, useEffect, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { gsap } from 'gsap'
import { Video, Music, FileAudio, Headphones, Rocket, Loader2, Info, Check, CheckCircle, XCircle, AlertTriangle, X } from 'lucide-react'
import SpotlightCard from '@/components/SpotlightCard'
import StarBorder from '@/components/StarBorder'
import ShinyText from '@/components/ShinyText'
import './App.css'

function App() {
  const [url, setUrl] = useState('')
  const [format, setFormat] = useState('mp3')
  const [isDownloading, setIsDownloading] = useState(false)
  const [progress, setProgress] = useState(0)
  const [progressText, setProgressText] = useState('')
  const [message, setMessage] = useState(null)
  const [showPlaylistWarning, setShowPlaylistWarning] = useState(false)
  const [toasts, setToasts] = useState([])
  const [isCheckingUrl, setIsCheckingUrl] = useState(false)
  const [urlResolved, setUrlResolved] = useState(false)
  const [legalModal, setLegalModal] = useState(null) // 'impressum', 'datenschutz', 'haftung', or null

  const eventSourceRef = useRef(null)
  const containerRef = useRef(null)
  const titleRef = useRef(null)
  const debounceTimerRef = useRef(null)
  const resolveTimerRef = useRef(null)
  const lastActionsRef = useRef([])

  // Detect iOS Safari
  const isIOSSafari = () => {
    const ua = navigator.userAgent
    const iOS = /iPad|iPhone|iPod/.test(ua) && !window.MSStream
    return iOS
  }

  // Session persistence: Check sessionStorage first, then generate new ID
  const getOrCreateSessionId = () => {
    const stored = sessionStorage.getItem('download_session_id')
    if (stored) return stored
    const newId = Math.random().toString(36).substring(7)
    sessionStorage.setItem('download_session_id', newId)
    return newId
  }
  const sessionIdRef = useRef(getOrCreateSessionId())

  // Track user actions for error reporting
  const trackAction = (action) => {
    const timestamp = new Date().toISOString()
    lastActionsRef.current = [
      ...lastActionsRef.current.slice(-9), // Keep last 9 actions
      `[${timestamp}] ${action}`
    ]
  }

  // Send error report to backend
  const reportError = useCallback(async (error, context = {}) => {
    try {
      const errorReport = {
        errorMessage: error.message || String(error),
        errorStack: error.stack || '',
        url: window.location.href,
        userAgent: navigator.userAgent,
        timestamp: new Date().toISOString(),
        sessionId: sessionIdRef.current,
        lastActions: lastActionsRef.current,
        browserInfo: {
          name: navigator.userAgentData?.brands?.[0]?.brand || 'Unknown',
          version: navigator.userAgentData?.brands?.[0]?.version || 'Unknown',
          os: navigator.userAgentData?.platform || navigator.platform || 'Unknown',
          language: navigator.language,
          screenResolution: `${window.screen.width}x${window.screen.height}`,
          viewport: `${window.innerWidth}x${window.innerHeight}`,
          ...context
        }
      }

      console.error('[ErrorReport] Sending error report:', errorReport)

      await fetch('/report-error', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(errorReport)
      })

      console.log('[ErrorReport] Error report sent successfully')
    } catch (err) {
      console.error('[ErrorReport] Failed to send error report:', err)
    }
  }, [])

  // Global error handler
  useEffect(() => {
    const handleError = (event) => {
      console.log('[ErrorHandler] Caught error:', event)
      reportError(event.error || new Error(event.message), {
        type: 'uncaught_error',
        filename: event.filename,
        lineno: event.lineno,
        colno: event.colno
      })
    }

    const handleUnhandledRejection = (event) => {
      console.log('[ErrorHandler] Caught unhandled rejection:', event)
      reportError(event.reason || new Error('Unhandled Promise Rejection'), {
        type: 'unhandled_rejection'
      })
    }

    window.addEventListener('error', handleError)
    window.addEventListener('unhandledrejection', handleUnhandledRejection)

    console.log('[ErrorHandler] Global error handlers registered')

    return () => {
      window.removeEventListener('error', handleError)
      window.removeEventListener('unhandledrejection', handleUnhandledRejection)
      console.log('[ErrorHandler] Global error handlers removed')
    }
  }, [reportError])

  // Validate if URL is from YouTube
  const isValidYouTubeURL = (url) => {
    if (!url) return false
    try {
      const urlObj = new URL(url)
      const host = urlObj.hostname.toLowerCase().replace(/^www\./, '')

      const validHosts = [
        'youtube.com',
        'm.youtube.com',
        'youtu.be',
        'youtube-nocookie.com'
      ]

      return validHosts.some(validHost =>
        host === validHost || host.endsWith('.' + validHost)
      )
    } catch {
      return false
    }
  }

  // Restore active download on page load
  useEffect(() => {
    const activeDownload = sessionStorage.getItem('active_download')
    if (activeDownload) {
      try {
        const { sessionID, url: savedUrl, format: savedFormat, timestamp } = JSON.parse(activeDownload)

        // Only restore if less than 10 minutes old
        const age = Date.now() - timestamp
        if (age < 10 * 60 * 1000) {
          console.log('[Restore] Attempting to restore download session:', sessionID)

          setUrl(savedUrl)
          setFormat(savedFormat)
          setIsDownloading(true)
          setProgress(0)
          setProgressText('Verbindung wird wiederhergestellt...')

          // Reconnect to SSE
          eventSourceRef.current = new EventSource(`/progress?session=${sessionID}`)

          eventSourceRef.current.onopen = () => {
            console.log('[Restore] SSE reconnected successfully')
            trackAction('SSE reconnected after page reload')
          }

          eventSourceRef.current.onmessage = (event) => {
            const update = JSON.parse(event.data)

            if (update.error === true || update.progress === -1) {
              eventSourceRef.current.close()
              sessionStorage.removeItem('active_download')
              setIsDownloading(false)
              setProgress(0)
              setProgressText('')
              setMessage({ type: 'error', text: update.status })
              addToast('error', update.status)
              return
            }

            setProgress(update.progress)
            setProgressText(update.status)

            if (update.progress === 100) {
              eventSourceRef.current.close()
              sessionStorage.removeItem('active_download')

              const filename = update.status.replace('Completed: ', '')
              triggerDownload(filename)

              setIsDownloading(false)
              setMessage({ type: 'success', text: 'Download abgeschlossen!' })
              setTimeout(() => {
                setProgress(0)
                setProgressText('')
              }, 300)
            }
          }

          eventSourceRef.current.onerror = (error) => {
            console.error('[Restore] SSE error on reconnect:', error)
            console.error('[Restore] ReadyState:', eventSourceRef.current?.readyState)

            // ReadyState 2 = CLOSED, means the channel doesn't exist anymore
            if (eventSourceRef.current?.readyState === 2) {
              console.log('[Restore] Channel closed, download likely completed or failed')
              eventSourceRef.current.close()
              sessionStorage.removeItem('active_download')
              setIsDownloading(false)
              setProgress(0)
              setProgressText('')
              setMessage({ type: 'info', text: 'Download wurde bereits abgeschlossen' })
            }
          }
        } else {
          // Too old, clear it
          sessionStorage.removeItem('active_download')
        }
      } catch (err) {
        console.error('[Restore] Failed to restore download:', err)
        sessionStorage.removeItem('active_download')
      }
    }
  }, [])

  // GSAP Animations on Mount
  useEffect(() => {
    const title = titleRef.current
    if (title) {
      const text = title.textContent
      title.innerHTML = text
        .split('')
        .map((char) => {
          if (char === ' ') return '<span class="char-space">&nbsp;</span>'
          return `<span class="char">${char}</span>`
        })
        .join('')
    }

    // Check if mobile device
    const isMobile = window.innerWidth <= 768

    const ctx = gsap.context(() => {
      // Split text animation - no opacity on mobile
      gsap.from('.char', {
        duration: isMobile ? 0.3 : 0.6,
        opacity: isMobile ? 1 : 0,
        y: isMobile ? 0 : 40,
        stagger: isMobile ? 0 : 0.03,
        ease: 'power3.out',
      })

      // Container animation - no opacity on mobile
      gsap.from(containerRef.current, {
        duration: isMobile ? 0.3 : 0.6,
        opacity: isMobile ? 1 : 0,
        ease: 'power3.out',
        delay: 0.1,
      })

      // Animate format cards - no opacity on mobile
      gsap.from('.format-card', {
        duration: isMobile ? 0.3 : 0.5,
        y: isMobile ? 0 : 20,
        stagger: isMobile ? 0 : 0.08,
        ease: 'power2.out',
        delay: isMobile ? 0 : 0.6,
      })
    })

    return () => ctx.revert()
  }, [])


  useEffect(() => {
    if (url.includes('&list=') || url.includes('?list=')) {
      setShowPlaylistWarning(true)
    } else {
      setShowPlaylistWarning(false)
    }
  }, [url])

  useEffect(() => {
    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
      }
      if (resolveTimerRef.current) {
        clearTimeout(resolveTimerRef.current)
      }
    }
  }, [])

  // URL Resolver - automatically resolve short links and canonicalize URLs
  useEffect(() => {
    if (resolveTimerRef.current) {
      clearTimeout(resolveTimerRef.current)
    }

    // Only resolve if URL looks like a YouTube URL
    if (url && (url.includes('youtube.com') || url.includes('youtu.be'))) {
      resolveTimerRef.current = setTimeout(async () => {
        try {
          const response = await fetch('/resolve', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({ url }),
          })

          const data = await response.json()

          if (data.success && data.resolvedUrl !== url) {
            // URL was resolved/changed - update it
            setUrl(data.resolvedUrl)
            setUrlResolved(true)

            // Show appropriate toast message
            if (data.wasRedirect) {
              addToast('success', '✓ Short-Link wurde aufgelöst')
            } else if (data.wasCanonical) {
              addToast('success', '✓ URL wurde in Standard-Format konvertiert')
            }

            // Reset resolved flag after 3 seconds
            setTimeout(() => setUrlResolved(false), 3000)
          }
        } catch (error) {
          console.error('URL resolution error:', error)
        }
      }, 300) // 300ms debounce for paste events
    }

    return () => {
      if (resolveTimerRef.current) {
        clearTimeout(resolveTimerRef.current)
      }
    }
  }, [url])

  // Auto URL check with debounce
  useEffect(() => {
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current)
    }

    // Only check if URL looks like a YouTube URL
    if (url && (url.includes('youtube.com') || url.includes('youtu.be'))) {
      setIsCheckingUrl(true)

      debounceTimerRef.current = setTimeout(async () => {
        try {
          const response = await fetch('/check-formats', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({ url, format }),
          })

          const data = await response.json()
          setIsCheckingUrl(false)

          if (data.success) {
            // Show success toast with quality info
            let toastMessage = 'URL gültig!'
            if (data.hasSABR && data.warnings.length > 0) {
              addToast('warning', `${toastMessage} (SABR aktiv - Qualität möglicherweise eingeschränkt)`)
            } else {
              addToast('success', toastMessage)
            }
          } else {
            addToast('error', 'Ungültige URL')
          }
        } catch (error) {
          setIsCheckingUrl(false)
          console.error('URL check error:', error)
        }
      }, 1000) // 1 second debounce
    } else {
      setIsCheckingUrl(false)
    }

    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current)
      }
    }
  }, [url, format])

  const addToast = (type, text) => {
    const id = Date.now()
    setToasts(prev => [...prev, { id, type, text }])

    // Auto remove after 5 seconds
    setTimeout(() => {
      setToasts(prev => prev.filter(toast => toast.id !== id))
    }, 5000)
  }

  const removeToast = (id) => {
    setToasts(prev => prev.filter(toast => toast.id !== id))
  }

  // Handle file download with iOS Safari compatibility
  const triggerDownload = (filename) => {
    const downloadUrl = `/download-file/${encodeURIComponent(filename)}`

    if (isIOSSafari()) {
      // iOS Safari: Open in new tab with instructions
      addToast('info', 'iOS: Datei wird in neuem Tab geöffnet. Tippe auf "Teilen" → "Datei sichern"')
      window.open(downloadUrl, '_blank')
      trackAction('iOS download: opened in new tab')
    } else {
      // Other browsers: Use download attribute
      const downloadLink = document.createElement('a')
      downloadLink.href = downloadUrl
      downloadLink.download = filename
      document.body.appendChild(downloadLink)
      downloadLink.click()
      document.body.removeChild(downloadLink)
      trackAction('Standard download triggered')
    }
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    trackAction(`Download initiated: format=${format}, url=${url.substring(0, 50)}...`)

    if (!url.trim()) {
      setMessage({ type: 'error', text: 'Bitte eine YouTube URL eingeben' })
      trackAction('Download failed: Empty URL')
      return
    }

    // Validate that URL is from YouTube
    if (!isValidYouTubeURL(url)) {
      addToast('error', 'Nur YouTube URLs sind erlaubt')
      setMessage({ type: 'error', text: 'Bitte verwende einen gültigen YouTube-Link (youtube.com, youtu.be)' })
      trackAction('Download failed: Invalid YouTube URL')
      return
    }

    setIsDownloading(true)
    setProgress(0)
    setProgressText('Starte Download...')
    setMessage(null)

    try {
      const response = await fetch('/download', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ url, format }),
      })

      // Check if response is OK
      if (!response.ok) {
        const errorText = await response.text()
        throw new Error(`HTTP ${response.status}: ${errorText}`)
      }

      const data = await response.json()

      if (data.success) {
        const sessionID = data.message
        trackAction(`SSE connection started: session=${sessionID}`)

        // Save active download to sessionStorage
        sessionStorage.setItem('active_download', JSON.stringify({
          sessionID,
          url,
          format,
          timestamp: Date.now()
        }))

        console.log('[SSE] Opening EventSource for session:', sessionID)
        eventSourceRef.current = new EventSource(`/progress?session=${sessionID}`)

        eventSourceRef.current.onopen = () => {
          console.log('[SSE] Connection opened successfully')
          trackAction('SSE connection opened')
        }

        eventSourceRef.current.onmessage = (event) => {
          console.log('[SSE] Message received:', event.data)
          const update = JSON.parse(event.data)

          // Check if this is an error
          if (update.error === true || update.progress === -1) {
            eventSourceRef.current.close()
            console.log('[SSE] Error received from backend:', update.status)
            trackAction(`Download failed: ${update.status}`)

            // Clean up session storage
            sessionStorage.removeItem('active_download')

            setIsDownloading(false)
            setProgress(0)
            setProgressText('')
            setMessage({ type: 'error', text: update.status })
            addToast('error', update.status)

            reportError(new Error(update.status), {
              type: 'backend_error',
              sessionID
            })
            return
          }

          setProgress(update.progress)
          setProgressText(update.status)

          if (update.progress === 100) {
            eventSourceRef.current.close()
            console.log('[SSE] Connection closed (100% reached)')
            trackAction(`Download completed: ${update.status}`)

            const filename = update.status.replace('Completed: ', '')
            console.log('[Download] Attempting download for:', filename)
            console.log('[Download] Encoded URL:', `/download-file/${encodeURIComponent(filename)}`)

            triggerDownload(filename)

            // Clean up session storage
            sessionStorage.removeItem('active_download')

            setIsDownloading(false)
            setMessage({ type: 'success', text: 'Download abgeschlossen!' })

            setTimeout(() => {
              setProgress(0)
              setProgressText('')
            }, 300)
          }
        }

        eventSourceRef.current.onerror = (error) => {
          console.error('[SSE] Error occurred:', error)
          console.error('[SSE] ReadyState:', eventSourceRef.current?.readyState)
          trackAction(`SSE error: readyState=${eventSourceRef.current?.readyState}`)
          reportError(new Error('SSE Connection Error'), {
            type: 'sse_error',
            readyState: eventSourceRef.current?.readyState,
            sessionID
          })
          eventSourceRef.current.close()

          // Clean up session storage
          sessionStorage.removeItem('active_download')

          setIsDownloading(false)
          setMessage({ type: 'error', text: 'Verbindungsfehler beim Fortschritt' })
        }
      } else {
        // Clean up session storage
        sessionStorage.removeItem('active_download')

        setIsDownloading(false)
        setMessage({ type: 'error', text: `Fehler: ${data.message}` })
        trackAction(`Download failed: ${data.message}`)
        reportError(new Error(data.message), { type: 'download_error', response: data })
      }
    } catch (error) {
      // Clean up session storage
      sessionStorage.removeItem('active_download')

      setIsDownloading(false)
      setMessage({ type: 'error', text: `Fehler: ${error.message}` })
      trackAction(`Download exception: ${error.message}`)
      reportError(error, { type: 'download_exception' })
    }
  }

  const formats = [
    { value: 'mp3', label: 'MP3', icon: Music, desc: 'Komprimiertes Audio' },
    { value: 'wav', label: 'WAV', icon: FileAudio, desc: 'Verlustfreies Audio' },
    { value: 'm4a', label: 'M4A', icon: Headphones, desc: 'Apple-kompatibel' },
    { value: 'mp4', label: 'MP4', icon: Video, desc: 'Beste Videoqualität' },
  ]

  const handleFormatClick = (fmt) => {
    if (isDownloading) return
    trackAction(`Format changed: ${format} → ${fmt.value}`)
    setFormat(fmt.value)
  }

  return (
    <>
      <div className="app">
        <div className="background-grid" />
        <div className="gradient-orbs">
          <div className="orb orb-1" />
          <div className="orb orb-2" />
          <div className="orb orb-3" />
        </div>

        <div ref={containerRef} className="container">
          <div className="glass-effect" />

          {/* Header */}
              <div className="header">
                <h1 ref={titleRef} className="title-gradient">YouTube Downloader</h1>
                <p className="subtitle">Videos und Audio in Top-Qualität herunterladen</p>
              </div>

          {/* Form */}
          <form onSubmit={handleSubmit}>
            <div className="form-group">
              <label htmlFor="url">YouTube Link</label>
              <div className="input-wrapper">
                <input
                  type="text"
                  id="url"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  placeholder="https://www.youtube.com/watch?v=... oder youtu.be/..."
                  disabled={isDownloading}
                  required
                  className={`animated-input ${urlResolved ? 'url-resolved' : ''}`}
                />
                <div className="input-glow" />
                {urlResolved && (
                  <motion.div
                    className="url-check-icon"
                    initial={{ scale: 0, rotate: -180 }}
                    animate={{ scale: 1, rotate: 0 }}
                    exit={{ scale: 0, rotate: 180 }}
                    transition={{ type: 'spring', damping: 15 }}
                  >
                    <CheckCircle size={20} />
                  </motion.div>
                )}
              </div>

              <AnimatePresence>
                {showPlaylistWarning && (
                  <motion.div
                    className="info"
                    initial={{ opacity: 0, height: 0, y: -10 }}
                    animate={{ opacity: 1, height: 'auto', y: 0 }}
                    exit={{ opacity: 0, height: 0, y: -10 }}
                    transition={{ duration: 0.3 }}
                  >
                    <Info size={16} />
                    <span>Playlist-Parameter werden automatisch entfernt</span>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>

            <div className="form-group format-selection">
              <label>Format auswählen</label>
              <div className="format-grid">
                {formats.map((fmt) => (
                  <SpotlightCard
                    key={fmt.value}
                    className={`format-card ${format === fmt.value ? 'selected' : ''}`}
                    onClick={() => handleFormatClick(fmt)}
                    spotlightColor="rgba(99, 102, 241, 0.3)"
                  >
                    <fmt.icon className="format-icon" />
                    <div className="format-label">
                      {format === fmt.value ? (
                        <ShinyText text={fmt.label} speed={3} />
                      ) : (
                        fmt.label
                      )}
                    </div>
                    <div className="format-desc">{fmt.desc}</div>
                    {format === fmt.value && (
                      <div className="selected-badge">
                        <Check size={14} strokeWidth={3} />
                      </div>
                    )}
                  </SpotlightCard>
                ))}
              </div>
            </div>

            <StarBorder>
              <motion.button
                type="submit"
                className="submit-button"
                disabled={isDownloading || isCheckingUrl}
                animate={isCheckingUrl ? {
                  scale: [1, 1.02, 1],
                  boxShadow: [
                    '0 0 0 0px rgba(139, 92, 246, 0)',
                    '0 0 0 8px rgba(139, 92, 246, 0.3)',
                    '0 0 0 0px rgba(139, 92, 246, 0)'
                  ]
                } : {}}
                transition={{
                  duration: 1.5,
                  repeat: isCheckingUrl ? Infinity : 0,
                  ease: "easeInOut"
                }}
              >
                <span className="button-text" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}>
                  {isDownloading ? (
                    <>
                      <Loader2 size={20} className="animate-spin" />
                      Läuft...
                    </>
                  ) : isCheckingUrl ? (
                    <>
                      <motion.div
                        animate={{ rotate: 360 }}
                        transition={{ duration: 2, repeat: Infinity, ease: "linear" }}
                      >
                        <Loader2 size={20} />
                      </motion.div>
                      Warte auf Prüfung...
                    </>
                  ) : (
                    <>
                      <Rocket size={20} />
                      Download starten
                    </>
                  )}
                </span>
              </motion.button>
            </StarBorder>
          </form>

          {/* Progress Bar */}
          <AnimatePresence>
            {isDownloading && (
              <motion.div
                className="progress-container"
                initial={{ opacity: 0, scale: 0.95 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.95 }}
                transition={{ duration: 0.3 }}
              >
                <div className="progress-bar-container">
                  <motion.div
                    className="progress-bar"
                    initial={{ width: 0 }}
                    animate={{ width: `${progress}%` }}
                    transition={{ duration: 0.5, ease: 'easeOut' }}
                  >
                    <div className="progress-wave" />
                  </motion.div>
                </div>
                <div className="progress-info">
                  <div className="progress-text">{progressText}</div>
                  <div className="progress-percentage">{progress}%</div>
                </div>
              </motion.div>
            )}
          </AnimatePresence>

          {/* Message */}
          <AnimatePresence>
            {message && (
              <motion.div
                className={`message ${message.type}`}
                initial={{ opacity: 0, y: 20, scale: 0.9 }}
                animate={{ opacity: 1, y: 0, scale: 1 }}
                exit={{ opacity: 0, y: -20, scale: 0.9 }}
                transition={{ type: 'spring', damping: 15 }}
              >
                {message.text}
              </motion.div>
            )}
          </AnimatePresence>

          {/* Footer */}
          <motion.div
            className="footer"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.5 }}
          >
            <button onClick={() => setLegalModal('impressum')} className="footer-link">
              Impressum
            </button>
            <span className="footer-separator">•</span>
            <button onClick={() => setLegalModal('datenschutz')} className="footer-link">
              Datenschutz
            </button>
            <span className="footer-separator">•</span>
            <button onClick={() => setLegalModal('haftung')} className="footer-link">
              Haftungsausschluss
            </button>
          </motion.div>
        </div>
      </div>

      {/* Legal Modal */}
      <AnimatePresence>
        {legalModal && (
          <>
            <motion.div
              className="modal-backdrop"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setLegalModal(null)}
            />
            <motion.div
              className="modal"
              initial={{ opacity: 0, scale: 0.85, y: 60, rotateX: 10 }}
              animate={{ opacity: 1, scale: 1, y: 0, rotateX: 0 }}
              exit={{ opacity: 0, scale: 0.9, y: 30, rotateX: -5 }}
              transition={{
                type: 'spring',
                damping: 20,
                stiffness: 300,
                mass: 0.8
              }}
            >
              <button className="modal-close" onClick={() => setLegalModal(null)}>
                <X size={24} />
              </button>

              <div className="modal-content">
                {legalModal === 'impressum' && (
                  <>
                    <h2>Impressum</h2>
                    <div className="legal-text">
                      <h3>Angaben gemäß § 5 TMG</h3>
                      <p>
                        [Dein Name]<br />
                        [Deine Straße und Hausnummer]<br />
                        [PLZ und Ort]
                      </p>

                      <h3>Kontakt</h3>
                      <p>
                        E-Mail: [deine@email.de]
                      </p>
                    </div>
                  </>
                )}

                {legalModal === 'datenschutz' && (
                  <>
                    <h2>Datenschutzerklärung</h2>
                    <div className="legal-text">
                      <h3>1. Datenschutz auf einen Blick</h3>
                      <p>
                        Diese Website verwendet keine Cookies und speichert keine personenbezogenen Daten dauerhaft.
                        SessionStorage wird nur temporär für die Funktionalität verwendet und wird beim Schließen des Browsers gelöscht.
                      </p>

                      <h3>2. Hosting</h3>
                      <p>
                        Diese Website wird gehostet bei [Dein Hosting-Provider].
                      </p>

                      <h3>3. Verwendete Technologien</h3>
                      <p>
                        - SessionStorage: Wird nur für aktive Downloads verwendet<br />
                        - Keine Cookies<br />
                        - Keine Tracking-Tools
                      </p>
                    </div>
                  </>
                )}

                {legalModal === 'haftung' && (
                  <>
                    <h2>Haftungsausschluss</h2>
                    <div className="legal-text">
                      <h3>Haftung für Inhalte</h3>
                      <p>
                        Die Inhalte unserer Seiten wurden mit größter Sorgfalt erstellt.
                        Für die Richtigkeit, Vollständigkeit und Aktualität der Inhalte können wir jedoch keine Gewähr übernehmen.
                      </p>

                      <h3>Urheberrecht</h3>
                      <p>
                        Bitte beachte die Urheberrechte der YouTube-Inhalte.
                        Downloads sind nur für privaten Gebrauch und mit Zustimmung des Urhebers erlaubt.
                      </p>

                      <h3>Nutzung</h3>
                      <p>
                        Die Nutzung dieses Tools erfolgt auf eigene Verantwortung.
                        Wir übernehmen keine Haftung für die heruntergeladenen Inhalte.
                      </p>
                    </div>
                  </>
                )}
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* Toast Container */}
      <div className="toast-container">
        <AnimatePresence>
          {toasts.map((toast) => (
            <motion.div
              key={toast.id}
              className={`toast toast-${toast.type}`}
              initial={{ opacity: 0, x: 300, scale: 0.8 }}
              animate={{ opacity: 1, x: 0, scale: 1 }}
              exit={{ opacity: 0, x: 300, scale: 0.8 }}
              transition={{ type: 'spring', damping: 20 }}
            >
              <div className="toast-content">
                <div className="toast-icon">
                  {toast.type === 'success' && <CheckCircle size={20} />}
                  {toast.type === 'error' && <XCircle size={20} />}
                  {toast.type === 'warning' && <AlertTriangle size={20} />}
                  {toast.type === 'info' && <Info size={20} />}
                </div>
                <div className="toast-text">{toast.text}</div>
              </div>
              <button className="toast-close" onClick={() => removeToast(toast.id)}>
                <X size={16} />
              </button>
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </>
  )
}

export default App
