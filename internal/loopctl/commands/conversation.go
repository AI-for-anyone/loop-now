package commands

import (
	"context"
	"net/url"
	"strconv"

	"loop-now/internal/loopctl/cli"
)

func addConversation(r *cli.Registry) {
	r.Add(&cli.Command{
		Path: []string{"conversation"}, Identities: []cli.Identity{cli.IdentityAgent}, Network: true, Retry: true,
		Summary: "Read your own conversation saved since the user last started over, whichever runtime handled it: requests and replies in full, each page oldest first; without a position, returns the newest page",
		Flags: []cli.Flag{
			{Name: "before", Type: cli.TypeString, Description: "Return the messages just before this message ID (older)"},
			{Name: "after", Type: cli.TypeString, Description: "Return the messages just after this message ID (newer)"},
			{Name: "limit", Type: cli.TypeInt, Min: intp(1), Max: intp(100), Description: "Messages per page, 1..100; default 20"},
		},
		Constraints: []cli.Constraint{{Kind: cli.AtMostOne, Flags: []string{"before", "after"}}},
		Output:      "ConversationPage (hasMore means there are older messages before this page; hasMoreAfter means there are newer messages after it)",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			q := url.Values{}
			for _, name := range []string{"before", "after"} {
				if inv.Has(name) {
					q.Set(name, inv.String(name))
				}
			}
			if inv.Has("limit") {
				q.Set("limit", strconv.Itoa(inv.Int("limit")))
			}
			return get(ctx, inv, q, "conversation")
		},
	})
}
