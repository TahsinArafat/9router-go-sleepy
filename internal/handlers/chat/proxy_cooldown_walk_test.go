package chat

import (
	"testing"
	"time"
)

func TestWalkIgnoresCooldownForCurrentTurn(t *testing.T) {
	resetProxyCooldownsForTest()
	t.Cleanup(resetProxyCooldownsForTest)

	urls := []string{
		"socks5://u:p@10.20.0.1:1080",
		"socks5://u:p@10.20.0.2:1080",
		"socks5://u:p@10.20.0.3:1080",
	}
	h, cleanup := newWalkRepo(t, "zz-cool", urls)
	defer cleanup()

	// Park every proxy, the way PR #1230 does after a run of 429s.
	for _, u := range urls {
		parkProxyCooling("opencode", u, 6*time.Hour, time.Now())
	}

	pool, err := h.Repo.GetProxyPool("zz-cool")
	if err != nil {
		t.Fatal(err)
	}
	connData := &ConnectionData{ProxyPoolID: "zz-cool", Provider: "opencode"}
	if _, err := h.getClientForConnection(connData); err != nil {
		t.Fatal(err)
	}

	got := h.selectProxyURL(connData, pool, true)
	if got == "" {
		t.Fatal("the walk stopped after one proxy because every proxy was cooling")
	}
	// The walk may progress through every proxy in the pool, even when all of
	// them were parked by earlier requests. Cooldown gates NEW requests; it must
	// not abort a walk already in progress, or a 350-proxy pool returns a 429
	// after trying exactly one egress.
	seen := map[string]bool{}
	for i := 0; i <= len(urls); i++ {
		if got == "" {
			break
		}
		seen[got] = true
		// The forward sets this on each pass; selectProxyURL alone does not.
		connData.ResolvedProxyURL = got
		refuseProxy(connData)
		got = h.selectProxyURL(connData, pool, true)
	}
	if len(seen) != len(urls) {
		t.Errorf("walk reached %d of %d proxies, want all", len(seen), len(urls))
	}
}
