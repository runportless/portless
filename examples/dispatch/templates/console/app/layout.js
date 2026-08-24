import './styles.css'

export const metadata = {
  title: 'Dispatch Control',
  description: 'Portless multi-checkout delivery example',
}

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  )
}

