// Package contexthandoff selects the part of an Agent's saved conversation that is given to a new runtime after a
// change of runtime, and writes it as a marked block that goes before the first message of the new session.
//
// The selection keeps messages whole: within a byte budget (one token counted per UTF-8 byte, which overestimates),
// the latest request, the latest answer and the first request come first, then the remaining messages from the newest
// back, skipping any that no longer fit. Kept messages stay in their original order with their role and full text, and
// the block says how many were left out, where, and how to read them (loopctl conversation). Reasoning, tool state and
// images are never carried; tool use is summarized per turn.
package contexthandoff

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Budget limits, in bytes (one token per UTF-8 byte).
const (
	DefaultBudget = 16000
	MinBudget     = 1024
	MaxBudget     = 64000
)

// ClampBudget returns a budget within MinBudget..MaxBudget; zero or less means DefaultBudget.
func ClampBudget(n int) int {
	if n <= 0 {
		return DefaultBudget
	}
	return min(max(n, MinBudget), MaxBudget)
}

// Message is one saved message of the conversation.
type Message struct {
	ID        uuid.UUID
	Role      string // user or assistant
	Origin    string // console, inbox_reminder or sleep_wake
	Status    string // the status of the turn it belongs to
	Text      string
	Images    int
	Tools     []Tool // the turn's tools, on a reply
	CreatedAt time.Time
}

// Tool is how often a turn called one tool and how many calls failed.
type Tool struct {
	Name   string
	Calls  int
	Failed int
}

// Block is the handoff block and what it carries.
type Block struct {
	Text     string // empty when there is nothing to carry
	Included int
	Omitted  int
	Images   int // images in the whole conversation (never carried)
	Bytes    int
}

const (
	openTag  = "<conversation-handoff>\n"
	closeTag = "</conversation-handoff>"
	// gapReserve is the most a "left out here" marker plus its separator can take.
	gapReserve = len("[999999 messages left out here]\n\n")
)

// Build selects the messages that fit the budget and writes the block; from and to are the runtimes' display names.
func Build(messages []Message, from, to string, budget int) Block {
	if len(messages) == 0 {
		return Block{}
	}
	images := 0
	for _, m := range messages {
		images += m.Images
	}
	total := len(messages)
	// The fixed part is sized with the widest counts, so the final text cannot exceed the budget.
	remaining := budget - len(openTag) - len(closeTag) - len(intro(from, to, total, total, total, images)) - gapReserve
	cost := make([]int, total)
	for i, m := range messages {
		cost[i] = len(render(m)) + gapReserve
	}
	selected := make([]bool, total)
	try := func(i int) {
		if i < 0 || selected[i] || cost[i] > remaining {
			return
		}
		selected[i] = true
		remaining -= cost[i]
	}
	try(lastIndex(messages, "user"))
	try(lastIndex(messages, "assistant"))
	try(firstIndex(messages, "user"))
	for i := total - 1; i >= 0; i-- {
		try(i)
	}
	included := 0
	for _, s := range selected {
		if s {
			included++
		}
	}
	var b strings.Builder
	b.WriteString(openTag)
	b.WriteString(intro(from, to, included, total, total-included, images))
	gap := 0
	for i, m := range messages {
		if !selected[i] {
			gap++
			continue
		}
		if gap > 0 {
			b.WriteString(gapLine(gap))
			gap = 0
		}
		b.WriteString(render(m))
	}
	if gap > 0 {
		b.WriteString(gapLine(gap))
	}
	b.WriteString(closeTag)
	return Block{Text: b.String(), Included: included, Omitted: total - included, Images: images, Bytes: b.Len()}
}

func intro(from, to string, included, total, omitted, images int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The runtime that runs you changed from %s to %s. You start a new session: below is the conversation saved before this message, "+
		"taken from the platform's record, not from your own memory. It is context, not a new request; answer the message that follows it.\n", from, to)
	if omitted == 0 {
		fmt.Fprintf(&b, "All %d saved messages are included in full. ", total)
	} else {
		fmt.Fprintf(&b, "%d of %d saved messages are included in full (the first request and the most recent ones); %d were left out to fit. ", included, total, omitted)
	}
	b.WriteString("Read any saved message with `loopctl conversation` (newest page) or `loopctl conversation --before <message id>`; each message below shows its id.\n")
	if images > 0 {
		fmt.Fprintf(&b, "Images (%d) are not carried. ", images)
	}
	b.WriteString("Reasoning and tool state are not carried; tool use is summarized per turn.\n\n")
	return b.String()
}

func render(m Message) string {
	label := m.Role
	switch {
	case m.Role == "user" && m.Origin == "inbox_reminder":
		label += ", platform reminder"
	case m.Role == "user" && m.Origin == "sleep_wake":
		label += ", sleep wake-up"
	case m.Role == "assistant" && (m.Status == "failed" || m.Status == "interrupted"):
		label += ", " + m.Status + " (partial)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s · %s · message %s", label, m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), m.ID)
	if len(m.Tools) > 0 {
		parts := make([]string, 0, len(m.Tools))
		for _, t := range m.Tools {
			p := fmt.Sprintf("%s ×%d", t.Name, t.Calls)
			if t.Failed > 0 {
				p += fmt.Sprintf(" (%d failed)", t.Failed)
			}
			parts = append(parts, p)
		}
		b.WriteString(" · tools: " + strings.Join(parts, ", "))
	}
	if m.Images > 0 {
		fmt.Fprintf(&b, " · %d images not carried", m.Images)
	}
	b.WriteString("]\n")
	b.WriteString(m.Text)
	b.WriteString("\n\n")
	return b.String()
}

func gapLine(n int) string {
	if n == 1 {
		return "[1 message left out here]\n\n"
	}
	return fmt.Sprintf("[%d messages left out here]\n\n", n)
}

func lastIndex(messages []Message, role string) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == role {
			return i
		}
	}
	return -1
}

func firstIndex(messages []Message, role string) int {
	for i, m := range messages {
		if m.Role == role {
			return i
		}
	}
	return -1
}
