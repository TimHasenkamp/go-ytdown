import { useEffect, useRef } from 'react'
import './star-border.css'

const StarBorder = ({
  children,
  className = '',
  color = 'white',
  speed = 5,
  as = 'div'
}) => {
  const canvasRef = useRef(null)
  const containerRef = useRef(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return

    const ctx = canvas.getContext('2d')
    let animationFrameId
    let mouseX = -1
    let mouseY = -1

    const resizeCanvas = () => {
      const rect = container.getBoundingClientRect()
      canvas.width = rect.width
      canvas.height = rect.height
    }

    const draw = () => {
      const rect = container.getBoundingClientRect()
      const width = rect.width
      const height = rect.height

      ctx.clearRect(0, 0, width, height)

      if (mouseX >= 0 && mouseY >= 0) {
        const gradient = ctx.createRadialGradient(mouseX, mouseY, 0, mouseX, mouseY, width / 2)
        gradient.addColorStop(0, color)
        gradient.addColorStop(1, 'transparent')

        ctx.fillStyle = gradient
        ctx.fillRect(0, 0, width, height)
      }

      animationFrameId = requestAnimationFrame(draw)
    }

    const handleMouseMove = (e) => {
      const rect = container.getBoundingClientRect()
      mouseX = e.clientX - rect.left
      mouseY = e.clientY - rect.top
    }

    const handleMouseLeave = () => {
      mouseX = -1
      mouseY = -1
    }

    resizeCanvas()
    window.addEventListener('resize', resizeCanvas)
    container.addEventListener('mousemove', handleMouseMove)
    container.addEventListener('mouseleave', handleMouseLeave)

    draw()

    return () => {
      window.removeEventListener('resize', resizeCanvas)
      container.removeEventListener('mousemove', handleMouseMove)
      container.removeEventListener('mouseleave', handleMouseLeave)
      cancelAnimationFrame(animationFrameId)
    }
  }, [color, speed])

  const Component = as

  return (
    <Component ref={containerRef} className={`star-border ${className}`}>
      <canvas ref={canvasRef} className="star-border-canvas" />
      <div className="star-border-content">{children}</div>
    </Component>
  )
}

export default StarBorder
