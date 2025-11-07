import { useState, useEffect, useRef } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { gsap } from 'gsap'
import { Video, Music, FileAudio, Headphones, Rocket, Loader2, Info, Check, CheckCircle, XCircle, AlertTriangle, X } from 'lucide-react'
import SpotlightCard from '@/components/SpotlightCard'
import StarBorder from '@/components/StarBorder'
import ShinyText from '@/components/ShinyText'
import './App.css'

function App() {
  const [currentPage, setCurrentPage] = useState('home')
  const [url, setUrl] = useState('')
  const [format, setFormat] = useState('mp3')
  const [isDownloading, setIsDownloading] = useState(false)
  const [progress, setProgress] = useState(0)
  const [progressText, setProgressText] = useState('')
  const [message, setMessage] = useState(null)
  const [showPlaylistWarning, setShowPlaylistWarning] = useState(false)
  const [toasts, setToasts] = useState([])
  const [isCheckingUrl, setIsCheckingUrl] = useState(false)

  const eventSourceRef = useRef(null)
  const containerRef = useRef(null)
  const titleRef = useRef(null)
  const debounceTimerRef = useRef(null)

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

    const ctx = gsap.context(() => {
      // Split text animation
      gsap.from('.char', {
        duration: 0.6,
        opacity: 0,
        y: 40,
        stagger: 0.03,
        ease: 'power3.out',
      })

      // Container animation
      gsap.from(containerRef.current, {
        duration: 0.6,
        opacity: 0,
        ease: 'power3.out',
        delay: 0.1,
      })

      // Animate format cards - removed opacity animation to fix visibility
      gsap.from('.format-card', {
        duration: 0.5,
        y: 20,
        stagger: 0.08,
        ease: 'power2.out',
        delay: 0.6,
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
    }
  }, [])

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

  const handleSubmit = async (e) => {
    e.preventDefault()

    if (!url.trim()) {
      setMessage({ type: 'error', text: 'Bitte eine YouTube URL eingeben' })
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

      const data = await response.json()

      if (data.success) {
        const sessionID = data.message

        eventSourceRef.current = new EventSource(`/progress?session=${sessionID}`)

        eventSourceRef.current.onmessage = (event) => {
          const update = JSON.parse(event.data)
          setProgress(update.progress)
          setProgressText(update.status)

          if (update.progress === 100) {
            eventSourceRef.current.close()

            const filename = update.status.replace('Completed: ', '')

            const downloadLink = document.createElement('a')
            downloadLink.href = `/download-file/${encodeURIComponent(filename)}`
            downloadLink.download = filename
            document.body.appendChild(downloadLink)
            downloadLink.click()
            document.body.removeChild(downloadLink)

            setIsDownloading(false)
            setMessage({ type: 'success', text: 'Download abgeschlossen!' })

            setTimeout(() => {
              setProgress(0)
              setProgressText('')
            }, 300)
          }
        }

        eventSourceRef.current.onerror = (error) => {
          console.error('EventSource error:', error)
          eventSourceRef.current.close()
          setIsDownloading(false)
          setMessage({ type: 'error', text: 'Verbindungsfehler beim Fortschritt' })
        }
      } else {
        setIsDownloading(false)
        setMessage({ type: 'error', text: `Fehler: ${data.message}` })
      }
    } catch (error) {
      setIsDownloading(false)
      setMessage({ type: 'error', text: `Fehler: ${error.message}` })
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

          {currentPage === 'home' && (
            <>
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
                  placeholder="https://www.youtube.com/watch?v=..."
                  disabled={isDownloading}
                  required
                  className="animated-input"
                />
                <div className="input-glow" />
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

            <div className="form-group" style={{ marginBottom: '32px' }}>
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
              <button
                type="submit"
                className="submit-button"
                disabled={isDownloading}
              >
                <span className="button-text" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '8px' }}>
                  {isDownloading ? (
                    <>
                      <Loader2 size={20} className="animate-spin" />
                      Läuft...
                    </>
                  ) : (
                    <>
                      <Rocket size={20} />
                      Download starten
                    </>
                  )}
                </span>
              </button>
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
            </>
          )}

          {currentPage === 'impressum' && (
            <div className="page-content">
              <h2>Impressum</h2>
              <p>Hier kommt das Impressum rein.</p>
            </div>
          )}

          {currentPage === 'disclaimer' && (
            <div className="page-content">
              <h2>Haftungsausschluss</h2>
              <p>Hier kommt der Haftungsausschluss rein.</p>
            </div>
          )}

          {currentPage === 'about' && (
            <div className="page-content">
              <h2>Über uns</h2>
              <p>Hier kommen Informationen über uns rein.</p>
            </div>
          )}
        </div>
      </div>

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
