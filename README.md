# ideas

A service for capturing and publishing bilingual (EN/ZH) idea posts to [changkun.de/ideas](https://changkun.de/ideas). It polishes, translates, and augments raw ideas using LLMs, then commits the result as markdown to GitHub.

## Architecture

```
Browser compose box ──POST──> API Server ──commit──> GitHub (changkun/blog)
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

Every instruction the service sends to an LLM lives in `prompts/` as a Go text
template, compiled into the binary with `go:embed`. A deployed server carries
its own prompts and cannot drift from the ones it was built with, and editing
what the model is told is a change to a text file rather than to Go source.

Callers authenticate with an access token from
[latere auth](https://auth.latere.ai), which the compose box on
changkun.de/ideas obtains through browser PKCE. The token is verified against
the issuer's JWKS and then checked against `AUTH_ALLOWED_PRINCIPALS`, since a
valid latere token only proves identity, not posting rights.

## API

```
GET  /ideas/ping       Health check (no auth)
POST /ideas/post       Submit an idea
POST /ideas/improve    Improve content without posting
```

All endpoints except `/ideas/ping` require an `Authorization: Bearer <latere
access token>` header. Anything else is answered with 401.

### POST /ideas/post

```json
{
  "title": "optional title",
  "content": "your idea content",
  "augmented": "optional pre-written augmentation"
}
```

### POST /ideas/improve

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
| `LLM_BASE_URL` | yes | — | LLM API base URL |
| `LLM_API_KEY` | yes | — | API key for the LLM service |
| `LLM_API_FORMAT` | no | auto | API shape: `openai` for `/chat/completions`, `anthropic` for `/v1/messages` |
| `GIT_TOKEN` | yes | — | GitHub personal access token |
| `LLM_MODEL` | no | `anthropic/claude-sonnet-4-5-20250929` | Model for augmentation and translation |
| `LLM_TITLE_MODEL` | no | `anthropic/claude-haiku-4-5-20251001` | Model for title, slug, and polish tasks |
| `GIT_REPO` | no | `changkun/blog` | Target GitHub repository |
| `GIT_COMMITTER_NAME` | no | `Changkun Ideas API Server` | Git commit author name |
| `GIT_COMMITTER_EMAIL` | no | `hi+ideas@changkun.de` | Git commit author email |
| `IDEAS_ADDR` | no | `0.0.0.0:80` | Server listen address |
| `AUTH_ALLOWED_PRINCIPALS` | no | — | Comma-separated emails / principal ids allowed to post with a latere token. Empty disables latere auth |
| `AUTH_URL` | no | `https://auth.latere.ai` | latere auth issuer |
| `AUTH_JWKS_URL` | no | `$AUTH_URL/.well-known/jwks.json` | JWKS document used to verify latere tokens |

Lux examples:

```bash
# OpenAI-compatible route.
LLM_BASE_URL=https://lux.latere.ai/openrouter/v1
LLM_API_FORMAT=openai

# Native Anthropic route.
LLM_BASE_URL=https://lux.latere.ai/anthropic
LLM_API_FORMAT=anthropic
```

## Deployment

```bash
make build   # Build Linux binary + Docker image
make up      # Start with docker compose
make down    # Stop
make clean   # Remove containers and images
```

## License

MIT
