import { useEffect } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { CheckCircle, XCircle, Info, X } from 'lucide-react'

const Toast = ({ toasts, removeToast }) => {
  const icons = {
    success: CheckCircle,
    error: XCircle,
    info: Info,
  }

  return (
    <div className="toast-container">
      <AnimatePresence>
        {toasts.map((toast) => {
          const Icon = icons[toast.type]

          return (
            <motion.div
              key={toast.id}
              className={`toast toast-${toast.type}`}
              initial={{ opacity: 0, y: -20, scale: 0.95 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: -20, scale: 0.95 }}
              transition={{ duration: 0.2 }}
            >
              <div className="toast-content">
                <Icon size={20} className="toast-icon" />
                <span className="toast-text">{toast.message}</span>
              </div>
              <button
                onClick={() => removeToast(toast.id)}
                className="toast-close"
                aria-label="Close notification"
              >
                <X size={16} />
              </button>
            </motion.div>
          )
        })}
      </AnimatePresence>
    </div>
  )
}

export default Toast
