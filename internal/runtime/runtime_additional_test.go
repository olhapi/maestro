package runtime

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTokenAndEnumHelpers(t *testing.T) {
	if got := normalizeToken("  --Claude-Code/v2.0!! "); got != "claude_code_v2_0" {
		t.Fatalf("unexpected normalized token: %q", got)
	}
	if got := normalizeToken("!!!"); got != "" {
		t.Fatalf("expected punctuation-only input to collapse to empty string, got %q", got)
	}

	values := map[string]string{
		"alpha_beta": "alpha-beta",
	}
	if got := normalizeEnum(" alpha beta ", "fallback", values); got != "alpha-beta" {
		t.Fatalf("unexpected normalized enum: %q", got)
	}
	if got := normalizeEnum("unknown", "fallback", values); got != "fallback" {
		t.Fatalf("expected fallback for unknown token, got %q", got)
	}

	if got, err := parseEnum("", "fallback", values, "demo kind"); err != nil || got != "fallback" {
		t.Fatalf("expected blank parse to fall back cleanly, got %q, %v", got, err)
	}
	if got, err := parseEnum(" alpha beta ", "fallback", values, "demo kind"); err != nil || got != "alpha-beta" {
		t.Fatalf("expected parse to normalize known token, got %q, %v", got, err)
	}
	if got, err := parseEnum(" nope ", "fallback", values, "demo kind"); err == nil || got != "fallback" || !strings.Contains(err.Error(), `unsupported demo kind "nope"`) {
		t.Fatalf("expected parse error for unknown token, got %q, %v", got, err)
	}
}

func TestBackendCapabilityParsing(t *testing.T) {
	if got := NormalizeBackend(" Claude-Code "); got != BackendClaude {
		t.Fatalf("unexpected normalized backend: %q", got)
	}
	if got, err := ParseBackend(" codex "); err != nil || got != BackendCodex {
		t.Fatalf("expected codex backend to parse, got %q, %v", got, err)
	}
	if got, err := ParseBackend(" nope "); err == nil || got != BackendUnknown || !strings.Contains(err.Error(), `unsupported backend "nope"`) {
		t.Fatalf("expected backend parse failure, got %q, %v", got, err)
	}

	if got := NormalizeCapability(" streaming events "); got != CapabilityStreamingEvents {
		t.Fatalf("unexpected normalized capability: %q", got)
	}
	if got, err := ParseCapability(" plan mode "); err != nil || got != CapabilityPlanMode {
		t.Fatalf("expected plan mode capability to parse, got %q, %v", got, err)
	}
	if got, err := ParseCapability(""); err != nil || got != CapabilityUnknown {
		t.Fatalf("expected blank capability to return the default, got %q, %v", got, err)
	}
	if got, err := ParseCapability("not-a-capability"); err == nil || got != CapabilityUnknown || !strings.Contains(err.Error(), `unsupported capability "not-a-capability"`) {
		t.Fatalf("expected capability parse failure, got %q, %v", got, err)
	}

	if got := NormalizeCapabilities(nil); got != nil {
		t.Fatalf("expected nil capability list to remain nil, got %+v", got)
	}
	if got := NormalizeCapabilities([]string{"unknown"}); got != nil {
		t.Fatalf("expected unknown-only capability list to collapse to nil, got %+v", got)
	}

	wantCapabilities := Capabilities{CapabilityPlanMode, CapabilityStreamingEvents, CapabilityUserInputRequests}
	if got := NormalizeCapabilities([]string{"user input requests", "plan mode", "streaming events", "plan mode", "streaming events"}); !reflect.DeepEqual(got, wantCapabilities) {
		t.Fatalf("unexpected normalized capabilities: got %+v want %+v", got, wantCapabilities)
	}
	if got, err := ParseCapabilities([]string{"user input requests", "plan mode", "streaming events", "plan mode"}); err != nil || !reflect.DeepEqual(got, wantCapabilities) {
		t.Fatalf("unexpected parsed capabilities: got %+v, %v want %+v", got, err, wantCapabilities)
	}
	if got, err := ParseCapabilities(nil); err != nil || got != nil {
		t.Fatalf("expected blank capability list to parse as nil, got %+v, %v", got, err)
	}
	if _, err := ParseCapabilities([]string{"plan mode", "bogus"}); err == nil || !strings.Contains(err.Error(), `unsupported capability "bogus"`) {
		t.Fatalf("expected invalid capability to fail, got %v", err)
	}
}

func TestPolicyAndInteractionEnums(t *testing.T) {
	if got := NormalizeAccessProfile(" FULL ACCESS "); got != AccessProfileFullAccess {
		t.Fatalf("unexpected normalized access profile: %q", got)
	}
	if got, err := ParseAccessProfile("plan then full access"); err != nil || got != AccessProfilePlanThenFullAccess {
		t.Fatalf("expected plan-then-full-access to parse, got %q, %v", got, err)
	}
	if got, err := ParseAccessProfile(""); err != nil || got != AccessProfileDefault {
		t.Fatalf("expected blank access profile to fall back, got %q, %v", got, err)
	}
	if got, err := ParseAccessProfile("bogus"); err == nil || got != AccessProfileDefault || !strings.Contains(err.Error(), `unsupported access profile "bogus"`) {
		t.Fatalf("expected access profile parse failure, got %q, %v", got, err)
	}

	if got := NormalizeStartupMode(" PLAN "); got != StartupModePlan {
		t.Fatalf("unexpected normalized startup mode: %q", got)
	}
	if got, err := ParseStartupMode(""); err != nil || got != StartupModeDefault {
		t.Fatalf("expected blank startup mode to fall back, got %q, %v", got, err)
	}
	if got, err := ParseStartupMode("burst"); err == nil || got != StartupModeDefault || !strings.Contains(err.Error(), `unsupported startup mode "burst"`) {
		t.Fatalf("expected startup mode parse failure, got %q, %v", got, err)
	}

	if got := NormalizeApprovalSurface(" file change "); got != ApprovalSurfaceFileEdit {
		t.Fatalf("unexpected normalized approval surface: %q", got)
	}
	if got, err := ParseApprovalSurface(""); err != nil || got != ApprovalSurfaceUnknown {
		t.Fatalf("expected blank approval surface to fall back, got %q, %v", got, err)
	}
	if got, err := ParseApprovalSurface("plan checkpoint"); err != nil || got != ApprovalSurfacePlanCheckpoint {
		t.Fatalf("expected plan checkpoint approval surface to parse, got %q, %v", got, err)
	}
	if got, err := ParseApprovalSurface("nope"); err == nil || got != ApprovalSurfaceUnknown || !strings.Contains(err.Error(), `unsupported approval surface "nope"`) {
		t.Fatalf("expected approval surface parse failure, got %q, %v", got, err)
	}

	if got := NormalizeEventKind(" turn completed "); got != EventKindTurnCompleted {
		t.Fatalf("unexpected normalized event kind: %q", got)
	}
	if got, err := ParseEventKind(""); err != nil || got != EventKindUnknown {
		t.Fatalf("expected blank event kind to fall back, got %q, %v", got, err)
	}
	if got, err := ParseEventKind("interaction resolved"); err != nil || got != EventKindInteractionResolved {
		t.Fatalf("expected interaction resolved event kind to parse, got %q, %v", got, err)
	}
	if got, err := ParseEventKind("bad event"); err == nil || got != EventKindUnknown || !strings.Contains(err.Error(), `unsupported event kind "bad event"`) {
		t.Fatalf("expected event kind parse failure, got %q, %v", got, err)
	}

	if got := NormalizeInteractionRequestKind(" command approval "); got != InteractionRequestKindApproveCommand {
		t.Fatalf("unexpected normalized interaction request kind: %q", got)
	}
	if got, err := ParseInteractionRequestKind(""); err != nil || got != InteractionRequestKindUnknown {
		t.Fatalf("expected blank interaction request kind to fall back, got %q, %v", got, err)
	}
	if got, err := ParseInteractionRequestKind("file edit approval"); err != nil || got != InteractionRequestKindApproveFileEdit {
		t.Fatalf("expected file edit approval kind to parse, got %q, %v", got, err)
	}
	if got, err := ParseInteractionRequestKind("unknown request"); err == nil || got != InteractionRequestKindUnknown || !strings.Contains(err.Error(), `unsupported interaction request kind "unknown request"`) {
		t.Fatalf("expected interaction request kind parse failure, got %q, %v", got, err)
	}

	requestKindCases := map[InteractionRequestKind]ApprovalSurface{
		InteractionRequestKindApproveCommand:        ApprovalSurfaceCommand,
		InteractionRequestKindApproveFileEdit:       ApprovalSurfaceFileEdit,
		InteractionRequestKindApproveProtectedWrite: ApprovalSurfaceProtectedWrite,
		InteractionRequestKindRequestUserInput:      ApprovalSurfaceUserInput,
		InteractionRequestKindPlanCheckpoint:        ApprovalSurfacePlanCheckpoint,
	}
	for kind, want := range requestKindCases {
		if got := kind.ApprovalSurface(); got != want {
			t.Fatalf("unexpected approval surface for %q: got %q want %q", kind, got, want)
		}
	}
	if got := InteractionRequestKind("other").ApprovalSurface(); got != ApprovalSurfaceUnknown {
		t.Fatalf("expected unknown request kind to map to unknown surface, got %q", got)
	}

	surfaceKindCases := map[ApprovalSurface]InteractionRequestKind{
		ApprovalSurfaceCommand:        InteractionRequestKindApproveCommand,
		ApprovalSurfaceFileEdit:       InteractionRequestKindApproveFileEdit,
		ApprovalSurfaceProtectedWrite: InteractionRequestKindApproveProtectedWrite,
		ApprovalSurfaceUserInput:      InteractionRequestKindRequestUserInput,
		ApprovalSurfacePlanCheckpoint: InteractionRequestKindPlanCheckpoint,
	}
	for surface, want := range surfaceKindCases {
		if got := surface.RequestKind(); got != want {
			t.Fatalf("unexpected request kind for %q: got %q want %q", surface, got, want)
		}
	}
	if got := ApprovalSurface("other").RequestKind(); got != InteractionRequestKindUnknown {
		t.Fatalf("expected unknown approval surface to map to unknown request kind, got %q", got)
	}
}
