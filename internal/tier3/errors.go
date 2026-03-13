package tier3

import "errors"

// errNoBrowser is returned by New when Chrome cannot be found on the system.
var errNoBrowser = errors.New("tier3: Chrome not found — install Chromium or Google Chrome")
