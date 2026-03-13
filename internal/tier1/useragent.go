package tier1

import "sync"

// userAgentPool holds a fixed set of realistic desktop browser UA strings and
// rotates through them in round-robin order. Safe for concurrent use.
type userAgentPool struct {
	agents []string
	mu     sync.Mutex
	idx    int
}

func newUserAgentPool() *userAgentPool {
	return &userAgentPool{
		agents: []string{
			// Chrome 124 on Windows 10
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
			// Firefox 125 on macOS
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.4; rv:125.0) Gecko/20100101 Firefox/125.0",
			// Safari 17 on macOS
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Safari/605.1.15",
			// Chrome 124 on macOS
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
			// Edge 124 on Windows 10
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 Edg/124.0.0.0",
		},
	}
}

// Next returns the next UA string in round-robin order.
func (p *userAgentPool) Next() string {
	p.mu.Lock()
	ua := p.agents[p.idx]
	p.idx = (p.idx + 1) % len(p.agents)
	p.mu.Unlock()
	return ua
}
