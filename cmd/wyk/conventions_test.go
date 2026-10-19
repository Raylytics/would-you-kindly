package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jimbottle/would-you-kindly/internal/hooks"
)

func TestConventions_BodyMentionsTheCanonicalLabels(t *testing.T) {
	// The whole point of this subcommand is to surface the labels
	// to agents. Verify they're all present in the prose form so a
	// future "tighten this up" edit can't accidentally drop them.
	body := conventionsBody
	for _, want := range []string{"human", "src:agent", "wyk handoff", "wyk inbox", "label=src:agent AND NOT label=human"} {
		if !strings.Contains(body, want) {
			t.Errorf("conventionsBody missing %q\n---\n%s", want, body)
		}
	}
}

func TestConventions_StructuredHasFixedSchema(t *testing.T) {
	// The JSON form is the agent-facing structured ingest. Schema
	// drift breaks tools that index on these keys, so pin them.
	c := conventionsStructured()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"labels"`,
		`"human"`,
		`"src:agent"`,
		`"src:human"`,
		`"queries"`,
		`"human_tasks"`,
		`"agent_inbox"`,
		`"preferred_command"`,
		`"bd_create_example"`,
		`"contract_url"`,
		`"lease"`,
		`"owner_key"`,
		`"until_key"`,
		`"pick_command"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("structured form missing key %s in:\n%s", want, s)
		}
	}
	if c.Queries.AgentInbox != "label=src:agent AND NOT label=human AND NOT label=agent-handoff AND status!=closed AND status!=blocked AND status!=deferred AND status!=hooked" {
		t.Errorf("agent inbox query drifted: %q", c.Queries.AgentInbox)
	}
}

func TestConventions_InboxRulePinned(t *testing.T) {
	// The "work inbox items now" imperative must be present in
	// both the structured form (agents may key on it) and the
	// prose body (readers learn the convention from the body
	// alone).
	c := conventionsStructured()
	if c.InboxRule == "" {
		t.Error("conventionsStructured.InboxRule must be non-empty")
	}
	for _, want := range []string{"wyk inbox", "work", "no longer blocking"} {
		if !strings.Contains(c.InboxRule, want) {
			t.Errorf("InboxRule missing %q; got %q", want, c.InboxRule)
		}
	}
	if !strings.Contains(conventionsBody, "Acting on the inbox") {
		t.Errorf("conventionsBody missing 'Acting on the inbox' heading")
	}
	// Case-insensitive on the imperative — the body shouts "WORK
	// them now" intentionally; future copy edits may lowercase.
	if !strings.Contains(strings.ToLower(conventionsBody), "work them now") {
		t.Errorf("conventionsBody missing the 'work them now' imperative")
	}
}

func TestConventions_StatusGuidancePinned(t *testing.T) {
	// Lifecycle order matters — agents may key on the index.
	// Pin both the status names and that deferred carries the
	// "WIP subsystem" guidance that triggered this contract bit.
	c := conventionsStructured()
	wantOrder := []string{"open", "in_progress", "blocked", "deferred", "closed"}
	if len(c.Statuses) != len(wantOrder) {
		t.Fatalf("expected %d statuses; got %d", len(wantOrder), len(c.Statuses))
	}
	for i, want := range wantOrder {
		if c.Statuses[i].Status != want {
			t.Errorf("statuses[%d].Status = %q, want %q", i, c.Statuses[i].Status, want)
		}
		if c.Statuses[i].When == "" {
			t.Errorf("statuses[%d] missing When guidance", i)
		}
	}
	// deferred specifically must mention the "not stabilised" /
	// "hidden from bd ready" framing — that's the rule the user
	// fed back to encode.
	deferred := c.Statuses[3]
	if !strings.Contains(deferred.When, "stabilis") || !strings.Contains(deferred.When, "ready") {
		t.Errorf("deferred guidance lost its core framing; got %q", deferred.When)
	}
}

func TestConventions_ProseBodyDocumentsStatusLifecycle(t *testing.T) {
	for _, want := range []string{
		"Status lifecycle",
		"open ",
		"in_progress",
		"blocked",
		"deferred",
		"closed ",
		"stabilised",
	} {
		if !strings.Contains(conventionsBody, want) {
			t.Errorf("conventionsBody missing lifecycle keyword %q", want)
		}
	}
}

func TestConventions_RunbookSectionsPinned(t *testing.T) {
	// Three required sections, in this order. Both the schema
	// shape and the literal headings matter — agent tooling
	// parses descriptions for these exact strings.
	c := conventionsStructured()
	wantHeadings := []string{
		"## Why this needs you (please confirm this is accurate)",
		"## Steps",
		"## What unblocks me when this returns",
	}
	if len(c.RunbookSections) != len(wantHeadings) {
		t.Fatalf("expected %d runbook sections; got %d", len(wantHeadings), len(c.RunbookSections))
	}
	for i, want := range wantHeadings {
		if c.RunbookSections[i].Heading != want {
			t.Errorf("section[%d].Heading = %q, want %q", i, c.RunbookSections[i].Heading, want)
		}
		if c.RunbookSections[i].Purpose == "" {
			t.Errorf("section[%d] missing Purpose", i)
		}
	}
}

func TestConventions_QuestionSectionsPinned(t *testing.T) {
	// The QUESTION shape shares the first and last section with the
	// task shape and swaps "## Steps" for "## Question" in the middle.
	// `wyk handoff` accepts either middle heading, so the structured
	// form must advertise both.
	c := conventionsStructured()
	wantHeadings := []string{
		"## Why this needs you (please confirm this is accurate)",
		"## Question",
		"## What unblocks me when this returns",
	}
	if len(c.QuestionSections) != len(wantHeadings) {
		t.Fatalf("expected %d question sections; got %d", len(wantHeadings), len(c.QuestionSections))
	}
	for i, want := range wantHeadings {
		if c.QuestionSections[i].Heading != want {
			t.Errorf("question[%d].Heading = %q, want %q", i, c.QuestionSections[i].Heading, want)
		}
		if c.QuestionSections[i].Purpose == "" {
			t.Errorf("question[%d] missing Purpose", i)
		}
	}
	// The headings the lint checks for must be the ones the
	// conventions teach — by construction, via the shared constants.
	if c.RunbookSections[1].Heading != runbookHeadingSteps || c.QuestionSections[1].Heading != runbookHeadingQuestion {
		t.Errorf("middle headings drifted from the lint constants: %q / %q", c.RunbookSections[1].Heading, c.QuestionSections[1].Heading)
	}
}

func TestConventions_ProseBodyDocumentsRequiredSections(t *testing.T) {
	// The human-readable body MUST mention all three section
	// headings — that's how a reader learns the convention from
	// `wyk conventions` alone, without having to dig into the
	// skill file or CONTRACT.md.
	for _, want := range []string{
		"## Why this needs you (please confirm this is accurate)",
		"## Steps",
		"## Question",
		"## What unblocks me when this returns",
		"-template",
		"-question",
	} {
		if !strings.Contains(conventionsBody, want) {
			t.Errorf("conventionsBody missing required section heading %q", want)
		}
	}
}

func TestConventions_QueriesAlignAcrossForms(t *testing.T) {
	// Both the prose body and the structured form now interpolate
	// the same agentInboxQuery / humanTasksQuery constants — pin
	// that they're literally the same string in both forms so a
	// future edit can't silently drift one side.
	c := conventionsStructured()
	if c.Queries.AgentInbox != agentInboxQuery {
		t.Errorf("structured agent_inbox = %q, want shared const %q", c.Queries.AgentInbox, agentInboxQuery)
	}
	if c.Queries.HumanTasks != humanTasksQuery {
		t.Errorf("structured human_tasks = %q, want shared const %q", c.Queries.HumanTasks, humanTasksQuery)
	}
	if !strings.Contains(conventionsBody, agentInboxQuery) {
		t.Errorf("prose form should embed the canonical agentInboxQuery (%q); got:\n%s", agentInboxQuery, conventionsBody)
	}
}

func TestConventions_RunDefaultIsText(t *testing.T) {
	if code := runConventions(nil); code != 0 {
		t.Errorf("runConventions(nil) = %d, want 0", code)
	}
}

func TestConventions_RunJSONExits0(t *testing.T) {
	if code := runConventions([]string{"-json"}); code != 0 {
		t.Errorf("runConventions(-json) = %d, want 0", code)
	}
}

// withHookConfig points the conventions command at a fixed hook config.
func withHookConfig(t *testing.T, cfg hooks.Config) {
	t.Helper()
	prev := newHandoffHookDispatcher
	newHandoffHookDispatcher = func() *hooks.Dispatcher { return &hooks.Dispatcher{Config: cfg} }
	t.Cleanup(func() { newHandoffHookDispatcher = prev })
}

// The hook section tells an agent, before it hands off, whether the
// handoff will also create a task elsewhere (would-you-kindly-2pwr.8).
func TestConventions_HandoffHookSection(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		withHookConfig(t, hooks.Config{})
		text := conventionsText(hooks.Config{})
		if !strings.Contains(text, "No handoff hook is configured") || !strings.Contains(text, "hooks.handoff.command") {
			t.Errorf("text form should say no hook is configured and how to set one:\n%s", text)
		}
		c := conventionsStructured()
		if c.HandoffHook.Configured || c.HandoffHook.Command != "" || c.HandoffHook.Events != nil {
			t.Errorf("structured hook = %+v, want unconfigured", c.HandoffHook)
		}
		if c.HandoffHook.ReplayCommand == "" || c.HandoffHook.DocsURL == "" || c.HandoffHook.FailureExitCode != exitHookFailed {
			t.Errorf("structured hook contract fields missing: %+v", c.HandoffHook)
		}
	})
	t.Run("configured", func(t *testing.T) {
		cfg := hooks.Config{Command: "node ~/bin/basicdo-hook.mjs", Events: []hooks.Event{hooks.EventHandoff, hooks.EventClose}}
		withHookConfig(t, cfg)
		text := conventionsText(cfg)
		for _, want := range []string{"IS configured", "node ~/bin/basicdo-hook.mjs", "events: handoff, close", "external_ref", "Exit 3", "wyk hook dispatch"} {
			if !strings.Contains(text, want) {
				t.Errorf("text form missing %q:\n%s", want, text)
			}
		}
		c := conventionsStructured()
		if !c.HandoffHook.Configured || c.HandoffHook.Command != cfg.Command {
			t.Errorf("structured hook = %+v, want configured with the command", c.HandoffHook)
		}
		if got := strings.Join(c.HandoffHook.Events, ","); got != "handoff,close" {
			t.Errorf("events = %q, want handoff,close", got)
		}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"handoff_hook"`) || !strings.Contains(string(b), `"configured":true`) {
			t.Errorf("JSON missing the handoff_hook key: %s", b)
		}
	})
	t.Run("all events when unfiltered, ping excluded", func(t *testing.T) {
		h := handoffHookStructured(hooks.Config{Command: "x"})
		if got := strings.Join(h.Events, ","); got != "handoff,bounce,close" {
			t.Errorf("events = %q, want handoff,bounce,close", got)
		}
	})
}
