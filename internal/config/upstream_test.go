package config

import (
	"testing"
)

// TestNextUpstreamRoundRobin verifies requests cycle through the enabled pool
// in order, skipping disabled/empty-key entries.
func TestNextUpstreamRoundRobin(t *testing.T) {
	c := Default()
	c.Upstreams = []Upstream{
		{BaseURL: "https://a.example", APIKey: "ka", Enabled: true},
		{BaseURL: "https://b.example", APIKey: "kb", Enabled: true},
		{BaseURL: "https://c.example", APIKey: "kc", Enabled: false}, // disabled, skipped
		{BaseURL: "https://d.example", APIKey: "", Enabled: true},    // empty key, skipped
	}

	var order []string
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		upstream, ok := c.NextUpstream()
		if !ok {
			t.Fatalf("request %d: expected ok", i)
		}
		base, key := upstream.BaseURL, upstream.APIKey
		seen[base]++
		// key must match the base
		want := "ka"
		if base == "https://b.example" {
			want = "kb"
		}
		if key != want {
			t.Errorf("base %s: got key %q want %q", base, key, want)
		}
		order = append(order, base)
	}
	wantOrder := []string{
		"https://a.example",
		"https://b.example",
		"https://a.example",
		"https://b.example",
		"https://a.example",
		"https://b.example",
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("round-robin order = %v, want %v", order, wantOrder)
		}
	}
	// 6 requests over 2 enabled upstreams → 3 each
	if seen["https://a.example"] != 3 || seen["https://b.example"] != 3 {
		t.Errorf("expected 3/3 across a and b, got %v", seen)
	}
	// disabled/empty must never be selected
	if _, hit := seen["https://c.example"]; hit {
		t.Errorf("disabled upstream c was selected")
	}
	if _, hit := seen["https://d.example"]; hit {
		t.Errorf("empty-key upstream d was selected")
	}
}

// TestNextUpstreamLegacyFallback confirms the pool-empty case falls back to the
// legacy single UpstreamBase/ZenAPIKey fields (so existing configs keep working).
func TestNextUpstreamLegacyFallback(t *testing.T) {
	c := Default()
	c.Upstreams = nil
	c.UpstreamBase = "https://legacy.example/"
	c.ZenAPIKey = "legacy-key"

	upstream, ok := c.NextUpstream()
	if !ok {
		t.Fatalf("expected ok via legacy fallback")
	}
	base, key := upstream.BaseURL, upstream.APIKey
	if base != "https://legacy.example" {
		t.Errorf("base = %q, want https://legacy.example (trailing slash trimmed)", base)
	}
	if key != "legacy-key" {
		t.Errorf("key = %q, want legacy-key", key)
	}
}

// TestNextUpstreamNoneConfigured returns ok=false when nothing is set.
func TestNextUpstreamNoneConfigured(t *testing.T) {
	c := Default()
	c.Upstreams = nil
	c.UpstreamBase = ""
	c.ZenAPIKey = ""
	if _, ok := c.NextUpstream(); ok {
		t.Errorf("expected ok=false with no upstream configured")
	}
}

func TestNextUpstreamSkipsCoolingUpstream(t *testing.T) {
	c := Default()
	c.Upstreams = []Upstream{
		{ID: "up_a", BaseURL: "https://a.example", APIKey: "ka", Enabled: true},
		{ID: "up_b", BaseURL: "https://b.example", APIKey: "kb", Enabled: true},
	}

	first, ok := c.NextUpstream()
	if !ok || first.ID != "up_a" {
		t.Fatalf("first upstream = %+v ok=%v, want up_a", first, ok)
	}
	c.ReportUpstreamResult(first, false, "rate limited")

	for i := 0; i < 3; i++ {
		next, ok := c.NextUpstream()
		if !ok {
			t.Fatalf("next %d: expected ok", i)
		}
		if next.ID != "up_b" {
			t.Fatalf("cooling upstream selected: got %+v, want up_b", next)
		}
	}

	c.ReportUpstreamResult(first, true, "")
	next, ok := c.NextUpstream()
	if !ok {
		t.Fatalf("expected ok after reset")
	}
	if next.ID != "up_a" {
		t.Fatalf("success did not clear cooldown, got %+v", next)
	}
}

func TestNextUpstreamReturnsFalseWhenAllUpstreamsCooling(t *testing.T) {
	c := Default()
	c.Upstreams = []Upstream{
		{ID: "up_a", BaseURL: "https://a.example", APIKey: "ka", Enabled: true},
		{ID: "up_b", BaseURL: "https://b.example", APIKey: "kb", Enabled: true},
	}

	a, ok := c.NextUpstream()
	if !ok {
		t.Fatal("expected first upstream")
	}
	b, ok := c.NextUpstream()
	if !ok {
		t.Fatal("expected second upstream")
	}
	c.ReportUpstreamResult(a, false, "bad gateway")
	c.ReportUpstreamResult(b, false, "bad gateway")

	if _, ok := c.NextUpstream(); ok {
		t.Fatal("expected ok=false while all upstreams are cooling")
	}
	if !c.HasConfiguredUpstream() {
		t.Fatal("configured upstreams should still be visible while cooling")
	}
}

// TestMigrateLegacyUpstream promotes the legacy pair into the pool exactly once.
func TestMigrateLegacyUpstream(t *testing.T) {
	c := Default()
	c.UpstreamBase = "https://opencode.ai/zen/go"
	c.ZenAPIKey = "sk-test"
	c.Upstreams = nil

	c.migrateLegacyUpstream()
	if len(c.Upstreams) != 1 {
		t.Fatalf("expected 1 upstream after migration, got %d", len(c.Upstreams))
	}
	u := c.Upstreams[0]
	if u.BaseURL != "https://opencode.ai/zen/go" || u.APIKey != "sk-test" || !u.Enabled {
		t.Errorf("migrated upstream wrong: %+v", u)
	}
	if u.OpenCodeGoWorkspaceID != DefaultOpenCodeGoWorkspaceID {
		t.Errorf("migrated OpenCode Go workspace = %q, want %q", u.OpenCodeGoWorkspaceID, DefaultOpenCodeGoWorkspaceID)
	}

	// Idempotent: running again must not duplicate.
	c.migrateLegacyUpstream()
	if len(c.Upstreams) != 1 {
		t.Errorf("migration not idempotent: got %d upstreams", len(c.Upstreams))
	}
}

// TestMigrateLegacyUpstreamSkipsWhenPoolPresent ensures we never clobber an
// existing pool with the legacy fields.
func TestMigrateLegacyUpstreamSkipsWhenPoolPresent(t *testing.T) {
	c := Default()
	c.UpstreamBase = "https://legacy.example"
	c.ZenAPIKey = "legacy-key"
	c.Upstreams = []Upstream{{BaseURL: "https://pool.example", APIKey: "pk", Enabled: true}}

	c.migrateLegacyUpstream()
	if len(c.Upstreams) != 1 || c.Upstreams[0].BaseURL != "https://pool.example" {
		t.Errorf("pool clobbered by migration: %+v", c.Upstreams)
	}
}

func TestApplyPatchPreservesOpenCodeGoQuotaSecretsAndVisibility(t *testing.T) {
	showRolling := true
	showWeekly := false
	showMonthly := true
	c := Default()
	c.Upstreams = []Upstream{{
		BaseURL:               "https://old.example/",
		APIKey:                "old-api-key",
		Enabled:               true,
		OpenCodeGoWorkspaceID: "old-workspace",
		OpenCodeGoAuthCookie:  "auth=old-cookie",
		OpenCodeGoShowRolling: &showRolling,
		OpenCodeGoShowWeekly:  &showWeekly,
		OpenCodeGoShowMonthly: &showMonthly,
	}}

	next := []Upstream{{
		BaseURL:               "https://new.example/",
		APIKey:                "",
		Enabled:               true,
		OpenCodeGoWorkspaceID: " new-workspace ",
		OpenCodeGoAuthCookie:  "",
	}}
	c.ApplyPatch(&Patch{Upstreams: &next})

	got := c.Snapshot().Upstreams[0]
	if got.APIKey != "old-api-key" {
		t.Fatalf("API key was not preserved: %+v", got)
	}
	if got.OpenCodeGoAuthCookie != "auth=old-cookie" {
		t.Fatalf("OpenCode Go cookie was not preserved: %+v", got)
	}
	if got.OpenCodeGoWorkspaceID != "new-workspace" {
		t.Fatalf("workspace was not trimmed/updated: %+v", got)
	}
	if got.OpenCodeGoShowRolling == nil || *got.OpenCodeGoShowRolling != true ||
		got.OpenCodeGoShowWeekly == nil || *got.OpenCodeGoShowWeekly != false ||
		got.OpenCodeGoShowMonthly == nil || *got.OpenCodeGoShowMonthly != true {
		t.Fatalf("OpenCode Go visibility flags were not preserved: %+v", got)
	}
}

func TestApplyPatchPreservesUpstreamSecretsByIDAfterDeleteAndReorder(t *testing.T) {
	c := Default()
	c.Upstreams = []Upstream{
		{ID: "up_a", BaseURL: "https://a.example", APIKey: "key-a", Enabled: true, OpenCodeGoAuthCookie: "auth=a"},
		{ID: "up_b", BaseURL: "https://b.example", APIKey: "key-b", Enabled: true, OpenCodeGoAuthCookie: "auth=b"},
		{ID: "up_c", BaseURL: "https://c.example", APIKey: "key-c", Enabled: true, OpenCodeGoAuthCookie: "auth=c"},
	}

	// Simulate the panel deleting A and reordering C before B. Blank secrets
	// mean "keep existing"; they must be matched by stable ID, not by index.
	next := []Upstream{
		{ID: "up_c", BaseURL: "https://c-new.example/", APIKey: "", Enabled: true, OpenCodeGoAuthCookie: ""},
		{ID: "up_b", BaseURL: "https://b-new.example/", APIKey: "", Enabled: true, OpenCodeGoAuthCookie: ""},
	}
	c.ApplyPatch(&Patch{Upstreams: &next})

	got := c.Snapshot().Upstreams
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "up_c" || got[0].APIKey != "key-c" || got[0].OpenCodeGoAuthCookie != "auth=c" {
		t.Fatalf("first row secret mismatch after reorder/delete: %+v", got[0])
	}
	if got[1].ID != "up_b" || got[1].APIKey != "key-b" || got[1].OpenCodeGoAuthCookie != "auth=b" {
		t.Fatalf("second row secret mismatch after reorder/delete: %+v", got[1])
	}
	for _, u := range got {
		if u.ID == "up_a" || u.APIKey == "key-a" || u.OpenCodeGoAuthCookie == "auth=a" {
			t.Fatalf("deleted upstream secret leaked into result: %+v", got)
		}
	}
}

func TestApplyPatchDoesNotPreserveDeletedSecretIntoNewIDLessRow(t *testing.T) {
	c := Default()
	c.Upstreams = []Upstream{
		{ID: "up_a", BaseURL: "https://a.example", APIKey: "key-a", Enabled: true, OpenCodeGoAuthCookie: "auth=a"},
		{ID: "up_b", BaseURL: "https://b.example", APIKey: "key-b", Enabled: true, OpenCodeGoAuthCookie: "auth=b"},
	}

	// Keep B by ID and add a new row without an ID. The new row must not
	// inherit A's secret just because it occupies A's old index.
	next := []Upstream{
		{BaseURL: "https://new.example", APIKey: "", Enabled: true, OpenCodeGoAuthCookie: ""},
		{ID: "up_b", BaseURL: "https://b-new.example", APIKey: "", Enabled: true, OpenCodeGoAuthCookie: ""},
	}
	c.ApplyPatch(&Patch{Upstreams: &next})

	got := c.Snapshot().Upstreams
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID == "" || got[0].ID == "up_a" || got[0].ID == "up_b" {
		t.Fatalf("new row did not get a fresh ID: %+v", got[0])
	}
	if got[0].APIKey != "" || got[0].OpenCodeGoAuthCookie != "" {
		t.Fatalf("new row inherited deleted secret: %+v", got[0])
	}
	if got[1].ID != "up_b" || got[1].APIKey != "key-b" || got[1].OpenCodeGoAuthCookie != "auth=b" {
		t.Fatalf("kept row secret mismatch: %+v", got[1])
	}
}

func TestApplyPatchDefaultsEmptyOpenCodeGoWorkspace(t *testing.T) {
	c := Default()
	next := []Upstream{{
		BaseURL:               "https://pool.example/",
		APIKey:                "pk",
		Enabled:               true,
		OpenCodeGoWorkspaceID: " ",
		OpenCodeGoAuthCookie:  "auth=cookie",
	}}

	c.ApplyPatch(&Patch{Upstreams: &next})
	got := c.Snapshot().Upstreams[0]
	if got.OpenCodeGoWorkspaceID != DefaultOpenCodeGoWorkspaceID {
		t.Fatalf("workspace = %q, want %q", got.OpenCodeGoWorkspaceID, DefaultOpenCodeGoWorkspaceID)
	}
}

func TestSnapshotDeepCopiesOpenCodeGoVisibilityPointers(t *testing.T) {
	showRolling := true
	c := Default()
	c.Upstreams = []Upstream{{
		BaseURL:               "https://pool.example",
		APIKey:                "pk",
		Enabled:               true,
		OpenCodeGoShowRolling: &showRolling,
	}}

	snap := c.Snapshot()
	*snap.Upstreams[0].OpenCodeGoShowRolling = false

	got := c.Snapshot().Upstreams[0]
	if got.OpenCodeGoShowRolling == nil || *got.OpenCodeGoShowRolling != true {
		t.Fatalf("snapshot mutation leaked into config: %+v", got)
	}
}

func TestApplyPatchDeepCopiesOpenCodeGoVisibilityPointers(t *testing.T) {
	showRolling := true
	c := Default()
	next := []Upstream{{
		BaseURL:               "https://pool.example",
		APIKey:                "pk",
		Enabled:               true,
		OpenCodeGoShowRolling: &showRolling,
	}}

	c.ApplyPatch(&Patch{Upstreams: &next})
	showRolling = false

	got := c.Snapshot().Upstreams[0]
	if got.OpenCodeGoShowRolling == nil || *got.OpenCodeGoShowRolling != true {
		t.Fatalf("patch pointer mutation leaked into config: %+v", got)
	}
}
