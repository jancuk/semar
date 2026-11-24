package mcpkit

import (
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxAuthorRunes bounds the client name that goes into a script: it is text from a peer.
const maxAuthorRunes = 80

// AgentAuthor is who wrote what an agent recorded over MCP, the same for the mobile, web, and journey
// tools: the instance is the agent, the harness is mcp, and the model is the name that the client gave in
// initialize, or "mcp" when it gave none. The name is cut to one line of at most 80 characters.
func AgentAuthor(client string) domain.ProviderRef {
	client = strings.Join(strings.Fields(client), " ")
	if r := []rune(client); len(r) > maxAuthorRunes {
		client = string(r[:maxAuthorRunes])
	}
	if client == "" {
		client = "mcp"
	}
	return domain.ProviderRef{Instance: "agent", Harness: "mcp", Model: client}
}
