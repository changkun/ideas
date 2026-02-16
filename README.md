# ideas

A service for capturing and publishing bilingual (EN/ZH) idea posts to [changkun.de/ideas](https://changkun.de/ideas). It polishes, translates, and augments raw ideas using LLMs, then commits the result as markdown to GitHub.

## Architecture

```
CLI (cmd/idea) ──POST──> API Server ──commit──> GitHub (changkun/blog)
                              │
                              └──> LLM API (polish, translate, augment, slug)
```

The server accepts a raw idea, then asynchronously:

1. Fetches any linked URLs for context
2. Generates a title if not provided
3. Detects language, polishes content, and translates to the other language
4. Generates a short URL slug
5. Augments with structured deep-dive content (Context, Key Insights, Open Questions)
6. Builds bilingual markdown with front matter
7. Commits to `content/ideas/` via GitHub API

Authentication is handled via [changkun.de/x/login](https://login.changkun.de) JWT tokens.

## Usage

### CLI

```bash
export LOGIN_USER=<username>
export LOGIN_PASS=<password>

# Interactive mode
go run ./cmd/idea

# With a title
go run ./cmd/idea -t "My Idea Title"

# Pipe from stdin
echo "Some interesting thought" | go run ./cmd/idea
```

Input controls (interactive mode):

- `Enter` — submit
- `Alt+Enter` or `Ctrl+J` — newline
- `Ctrl+W` — delete word
- `Ctrl+U` — clear all
- `Ctrl+C` — cancel

### API

```
GET  /ideas/ping       Health check (no auth)
POST /ideas/post       Submit an idea
POST /ideas/improve    Improve content without posting
```

All endpoints except `/ideas/ping` require a Bearer token or login cookie.

#### POST /ideas/post

```json
{
  "title": "optional title",
  "content": "your idea content",
  "augmented": "optional pre-written augmentation"
}
```

#### POST /ideas/improve

```json
{
  "content": "text to improve"
}
```

Returns `{"ok": true, "content": "improved text"}`.

## Configuration

Copy `.env.template` to `.env` and fill in the values:

| Variable | Required | Default | Description |
|---|---|---|---|
| `LLM_BASE_URL` | yes | — | OpenAI-compatible API base URL |
| `LLM_API_KEY` | yes | — | API key for the LLM service |
| `GIT_TOKEN` | yes | — | GitHub personal access token |
| `LLM_MODEL` | no | `anthropic/claude-sonnet-4-5-20250929` | Model for augmentation and translation |
| `LLM_TITLE_MODEL` | no | `anthropic/claude-haiku-4-5-20251001` | Model for title, slug, and polish tasks |
| `GIT_REPO` | no | `changkun/blog` | Target GitHub repository |
| `GIT_COMMITTER_NAME` | no | `Changkun Ideas API Server` | Git commit author name |
| `GIT_COMMITTER_EMAIL` | no | `hi+ideas@changkun.de` | Git commit author email |
| `IDEAS_ADDR` | no | `0.0.0.0:80` | Server listen address |
| `LOGIN_VERIFY_URL` | no | `https://login.changkun.de/verify` | Login service verify endpoint |

CLI-specific variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `LOGIN_USER` | yes | — | Login username |
| `LOGIN_PASS` | yes | — | Login password |
| `IDEAS_URL` | no | `https://api.changkun.de` | Ideas API base URL |
| `LOGIN_URL` | no | `https://login.changkun.de` | Login service URL |

## Deployment

```bash
make build   # Build Linux binary + Docker image
make up      # Start with docker compose
make down    # Stop
make clean   # Remove containers and images
```

## License

MIT
