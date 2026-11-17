package chat

import (
	"net/http"
	"testing"

	"9router/proxy/internal/db"
)

// newWalkRepo builds a handler with one pool of the given URLs and a connection
// bound to it — the shape a hundred-proxy round-robin pool actually has.
func newWalkRepo(t *testing.T, poolID string, urls []string) (*ChatHandler, func()) {
	t.Helper()

	database, cleanup := setupChatTestDB(t)
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`); err != nil {
		cleanup()
		t.Fatalf("create proxyPools: %v", err)
	}

	data := `{"name":"pool","urls":[`
	for i, u := range urls {
		if i > 0 {
			data += ","
		}
		data += `"` + u + `"`
	}
	data += `],"type":"socks5","strategy":"round-robin"}`
	if _, err := database.Exec(`INSERT INTO proxyPools (id, isActive, testStatus, data, createdAt, updatedAt)
		VALUES (?, 1, 'unknown', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		poolID, data); err != nil {
		cleanup()
		t.Fatalf("insert pool: %v", err)
	}

	return &ChatHandler{Repo: db.NewRepo(database), Client: &http.Client{}}, cleanup
}

// The core of the bug: a pool is many egresses behind ONE connection. Resolving
// once per turn meant a refused proxy was never walked past, so a hundred-proxy
// pool produced one attempt and the turn failed.
func TestResolveWalksPastARefusedProxy(t *testing.T) {
	urls := []string{
		"socks5://u:p@10.0.0.1:1080",
		"socks5://u:p@10.0.0.2:1080",
		"socks5://u:p@10.0.0.3:1080",
	}
	h, cleanup := newWalkRepo(t, "walk-pool-1", urls)
	defer cleanup()

	connData := &ConnectionData{ProxyPoolID: "walk-pool-1"}
	first, err := h.getClientForConnection(connData)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	firstURL := proxyURLOf(t, first)
	if firstURL == "" {
		t.Fatal("first resolve dialled directly, want a proxy")
	}

	refuseProxy(connData)

	second, err := h.getClientForConnection(connData)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	// NextURL pre-increments, so which URL comes first is not fixed; what
	// matters is that the refused one is not handed back.
	if got := proxyURLOf(t, second); got == firstURL {
		t.Errorf("after one refusal the resolver dialled %q again, want a different proxy", got)
	}
}

// With every proxy refused the resolver reports exhaustion rather than silently
// reusing a spent one. The caller turns that into the last upstream refusal, so
// the operator sees what the gate said.
func TestResolveReportsExhaustionWhenEveryProxyIsRefused(t *testing.T) {
	urls := []string{
		"socks5://u:p@10.0.0.1:1080",
		"socks5://u:p@10.0.0.2:1080",
	}
	h, cleanup := newWalkRepo(t, "walk-pool-2", urls)
	defer cleanup()

	connData := &ConnectionData{ProxyPoolID: "walk-pool-2"}
	for range urls {
		if _, err := h.getClientForConnection(connData); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		refuseProxy(connData)
	}
	// Every URL is now refused, so a further selection has nothing to offer.

	pool, err := h.Repo.GetProxyPool("walk-pool-2")
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if got := h.nextProxyURL(connData, pool); got != "" {
		t.Errorf("nextProxyURL() = %q, want \"\" once every proxy is spent", got)
	}
}

// A pool with no refused proxies keeps the plain rotation, so a caller that
// never records a refusal sees exactly the old behaviour.
func TestResolveWithoutRefusalsIsPlainRotation(t *testing.T) {
	urls := []string{
		"socks5://u:p@10.0.0.1:1080",
		"socks5://u:p@10.0.0.2:1080",
	}
	h, cleanup := newWalkRepo(t, "walk-pool-3", urls)
	defer cleanup()

	pool, err := h.Repo.GetProxyPool("walk-pool-3")
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	connData := &ConnectionData{ProxyPoolID: "walk-pool-3"}

	allowed := map[string]bool{}
	for _, u := range urls {
		allowed[u] = true
	}

	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		got := h.nextProxyURL(connData, pool)
		if got == "" {
			t.Fatal("nextProxyURL() = \"\" with no refusals recorded")
		}
		if !allowed[got] {
			t.Fatalf("nextProxyURL() = %q, which is not in the pool", got)
		}
		seen[got] = true
	}
	if len(seen) != 2 {
		t.Errorf("walked %d distinct proxies over 4 draws, want both", len(seen))
	}
}

// Refusals are tracked per pool, so one pool's spent proxies never affect
// another pool's selection.
func TestRefusalsAreScopedPerPool(t *testing.T) {
	urls := []string{
		"socks5://u:p@10.0.0.1:1080",
		"socks5://u:p@10.0.0.2:1080",
	}
	h, cleanup := newWalkRepo(t, "walk-pool-4", urls)
	defer cleanup()

	connData := &ConnectionData{ProxyPoolID: "walk-pool-4"}
	if _, err := h.getClientForConnection(connData); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	refuseProxy(connData)

	// A different pool id must not see the refusal.
	other := &ConnectionData{ProxyPoolID: "other-pool", RefusedProxies: connData.RefusedProxies}
	pool, err := h.Repo.GetProxyPool("walk-pool-4")
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	got := h.nextProxyURL(other, pool)
	if got == "" {
		t.Error("a refusal in one pool must not exhaust another pool")
	}
}
