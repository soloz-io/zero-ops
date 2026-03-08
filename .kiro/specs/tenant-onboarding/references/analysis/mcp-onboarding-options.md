Yes — what you’re describing is a known pain point in the MCP ecosystem:

Users must manually copy API keys from a browser and paste them into mcp.json.

There are better patterns, and some tools already implement agentic / automated setup workflows. Let’s break down the current approaches.

⸻

1. Current standard workflow (manual API key)

Most MCP clients like Cursor or Claude Code use a config file such as:

{
  "mcpServers": {
    "github": {
      "url": "https://api.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <API_KEY>"
      }
    }
  }
}

The API key or token is typically passed:
	•	via headers
	•	or via environment variables.  ￼

This means the user usually:
	1.	Open provider website
	2.	Generate API key
	3.	Paste it into mcp.json

So yes — your frustration is common.

⸻

2. Better workflow: OAuth login (browser auth)

The more modern pattern is OAuth login.

Example MCP config:

{
  "mcpServers": {
    "superset": {
      "type": "http",
      "url": "https://api.example.com/mcp",
      "oauth": {
        "authorizationUrl": "...",
        "tokenUrl": "..."
      }
    }
  }
}

Workflow:

IDE → open OAuth login
User signs in
Token stored automatically

Some MCP servers already support OAuth authentication instead of API keys.  ￼

This eliminates manual key copying.

⸻

3. Setup wizard (best developer UX)

Some MCP platforms provide a CLI setup wizard that:
	•	authenticates user
	•	generates API key
	•	writes the MCP config automatically.

Example workflow:

curl https://example.com/scripts/mcp.sh | bash

The script:
	1.	logs the user in
	2.	creates API keys
	3.	writes mcp.json automatically.  ￼

This is currently one of the best onboarding UX patterns.

⸻

4. “Add to Cursor” button (tool marketplace model)

Another emerging pattern is:

One-click installation

Cursor supports:

Add to Cursor

buttons that automatically install MCP servers and authenticate if needed.  ￼

Workflow:

Website
  ↓
Add to Cursor button
  ↓
Cursor installs MCP
  ↓
OAuth login

This is similar to “Install VSCode extension”.

⸻

5. Agent-driven onboarding (what you asked about)

Yes — theoretically an agentic chat could do this.

Example UX:

User: Install GitHub MCP

Agent:
- detects MCP server
- opens OAuth login
- writes mcp.json
- verifies connection

Architecture:

Agent
  ↓
MCP Registry
  ↓
Install server
  ↓
Run OAuth flow
  ↓
Write config

Cursor actually allows programmatic registration of MCP servers via extensions, which enables automated setup workflows without editing mcp.json manually.  ￼

⸻

6. Reference workflow architecture

A modern MCP onboarding flow should look like this:

Agent
  ↓
Search MCP registry
  ↓
Install server
  ↓
OAuth / Device login
  ↓
Store token in secrets
  ↓
Generate mcp.json

Key pieces:

Component	Purpose
MCP registry	discover servers
OAuth	secure auth
Secrets store	API keys
Agent installer	config generation


⸻

7. Best existing references

These are the closest examples of good UX:

Pattern	Example
OAuth MCP	Superset MCP
Setup wizard	ContextStream
One-click install	Cursor MCP marketplace
CLI install	Claude Code MCP add


⸻

8. What the ecosystem is missing

There is still no universal MCP package manager.

What we likely need:

mcp install github
mcp login github

Similar to:

npm install
pip install

Many teams are experimenting with “MCP registries” for this.

⸻

✅ Short answer

Yes — the better patterns are:
	1.	OAuth login
	2.	CLI setup wizard
	3.	“Add to IDE” install buttons
	4.	Agent-driven install flows

Manual API key pasting is the old pattern.