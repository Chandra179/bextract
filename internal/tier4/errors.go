package tier4

import "errors"

// errNoBrowserless is returned by New when BrowserlessURL is not configured.
var errNoBrowserless = errors.New("tier4: BrowserlessURL not configured")
