# AI controller — jadeck add-on

Talk to the booth in plain words: "go to the pulpit shot", "next slide",
"in 5 minutes cut to camera 2". The assistant works through the
controller's own agent tools (the same ones the MCP server offers) and
shows each change before doing it.

- **Your AI service, your key.** Anthropic (Claude, default model
  `claude-opus-5-5`), OpenAI, Google Gemini, Ollama on your own network, or
  any OpenAI-compatible service.
- **Only what you tick.** The connection's "It may" list is the set of
  permissions the assistant gets. Audio mutes are off by default. It can
  never install add-ons or apply updates.
- **Ask before acting** (on by default): reading status runs at once;
  anything that changes something waits for **Do it**.
- **Later**: "in 5 minutes…" or "at 10:35…" becomes a scheduled action that
  every screen shows under **Coming up**, where anyone can run it now or
  cancel it.

What you type, and the status the assistant reads, go to the AI service you
pick. Use Ollama to keep everything on your network.

## Use it

1. Settings → Add-ons → Browse → AI controller → Install.
2. Settings → Connections → Add → AI controller. Pick the service, paste
   your API key, tick what it may do, Test.
3. Edit a dashboard → add the **Assistant** widget (and **Coming up**).

## Build

```bash
go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon build
```

Try a local build: Settings → Add-ons → Developer → Load a folder (this
folder), or Install from file with `dist/ai-controller.ptzaddon`.

Release: push a tag `v1.2.3`; the workflow builds, signs (secret
`PTZ_SIGNING_KEY`) and attaches `ai-controller.ptzaddon` to the release.
