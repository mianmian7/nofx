import { motion } from 'framer-motion'
import { X } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { t, Language } from '../../i18n/translations'
interface LoginModalProps {
  onClose: () => void
  language: Language
}

export default function LoginModal({ onClose, language }: LoginModalProps) {
  const navigate = useNavigate()

  return (
    <motion.div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      onClick={onClose}
    >
      <motion.div
        className="relative max-w-md w-full rounded-2xl p-8 bg-nofx-bg-lighter border border-nofx-border shadow-2xl"
        initial={{ scale: 0.9, y: 50 }}
        animate={{ scale: 1, y: 0 }}
        exit={{ scale: 0.9, y: 50 }}
        onClick={(e) => e.stopPropagation()}
      >
        <motion.button
          onClick={onClose}
          className="absolute top-4 right-4 text-nofx-text-muted hover:text-nofx-text"
          whileHover={{ scale: 1.1, rotate: 90 }}
          whileTap={{ scale: 0.9 }}
        >
          <X className="w-6 h-6" />
        </motion.button>
        <h2 className="text-2xl font-bold mb-6 text-nofx-text">
          {t('accessNofxPlatform', language)}
        </h2>
        <p className="text-sm mb-6 text-nofx-text-muted">
          {t('loginRegisterPrompt', language)}
        </p>
        <div className="space-y-3">
          <motion.button
            onClick={() => {
              navigate('/login')
              onClose()
            }}
            className="block w-full px-6 py-3 rounded-lg font-semibold text-center bg-nofx-gold text-nofx-bg hover:bg-nofx-gold-highlight transition-all"
            whileHover={{
              scale: 1.02,
              boxShadow: '0 10px 30px var(--nofx-gold-glow)',
            }}
            whileTap={{ scale: 0.98 }}
          >
            {t('signIn', language)}
          </motion.button>
        </div>
      </motion.div>
    </motion.div>
  )
}
