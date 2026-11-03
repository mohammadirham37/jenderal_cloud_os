// Jenderal Cloud OS remote browser defaults (small servers: few processes,
// no telemetry, no first-run pages).
pref("browser.startup.homepage", "https://duckduckgo.com/");
pref("browser.startup.page", 1);
pref("browser.aboutwelcome.enabled", false);
pref("browser.shell.checkDefaultBrowser", false);
pref("dom.ipc.processCount", 2);
pref("dom.ipc.processCount.webIsolated", 1);
pref("fission.autostart", false);
pref("browser.sessionstore.interval", 60000);
pref("browser.cache.disk.capacity", 102400);
pref("media.hardware-video-decoding.enabled", false);
pref("gfx.webrender.software", true);
pref("toolkit.telemetry.enabled", false);
pref("datareporting.healthreport.uploadEnabled", false);
pref("datareporting.policy.dataSubmissionEnabled", false);
pref("browser.download.dir", "/home/jenderal/Downloads");
pref("browser.download.folderList", 2);
