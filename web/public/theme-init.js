(function () {
  var preference = 'system'
  try {
    var stored = window.localStorage.getItem('portless.theme')
    if (stored === 'light' || stored === 'dark' || stored === 'system') preference = stored
  } catch (_) {
    // Browser storage is optional; system preference remains the fallback.
  }
  var resolved = preference === 'system'
    ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    : preference
  document.documentElement.dataset.theme = resolved
  document.documentElement.style.colorScheme = resolved
  var themeColor = document.querySelector('meta[name="theme-color"]')
  if (themeColor) themeColor.setAttribute('content', resolved === 'light' ? '#f4f7f7' : '#071012')
})()
