// Reloads the extension from disk. Triggered by navigating to reload.html.
// Must be an external file: MV3's default extension-page CSP (script-src 'self')
// blocks inline <script>, so an inline reload call silently never runs.
chrome.runtime.reload();
