# Changelog

## 1.0.2

- **Fix:** a crash / data race when the Assistant widget refreshed while a
  reply was still being worked out. The conversation is now read and
  written under a lock, and one turn runs at a time per conversation (a
  double tap on Send or Do it waits for the first instead of corrupting
  the history).
- **Fix:** malformed action parameters are reported instead of being
  ignored, and a bad OpenAI-compatible service address is an error
  instead of a crash.
- Built with add-on SDK v0.3.0.

## 1.0.1

- Renamed to jadeck (SDK v0.2.0).

## 1.0.0

- Assistant: chat with Claude or an OpenAI-compatible model that drives
  the controller through its tools, asking first before anything changes.
