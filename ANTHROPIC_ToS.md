# Anthropic Terms of Service — Compliance Note

## For Anthropic

I've read the Consumer Terms of Service and the Claude Code Legal and Compliance page. This project does not use the Agent SDK and does not extract or reuse OAuth tokens outside of the `claude` binary. I respect these requirements.

The reason I'm careful about this: I understand that chat, Cowork, Code, and API represent different tiers of inference quality. Subscription plans subsidize accessibility. The Agent SDK on Max would let someone consume high-quality, minimally quantized tokens at flat-rate pricing — that's not sustainable, and I'm not going to pretend I don't see it.

This project is a Telegram interface to Claude Code — each user runs it locally, with their own subscription and their own `claude` binary. It spawns `claude -p` directly — your binary, your auth, your rate limits.

Technically, this is what happens:

```
Telegram message → pink-agent → claude -p --input-format stream-json --output-format stream-json --resume <id> → response → Telegram
```

No API calls. No OAuth tokens extracted or reused outside the `claude` binary. No SDK. No proxy. No multi-tenancy.

I love Claude Code, and Opus's critical thinking and moral reasoning in particular. My bet is on Anthropic — this project exists because I want more people using your tools and paying for subscriptions. You're the only company offering flat-rate access to frontier models — that's exactly why I'm here and not somewhere else.

If this crosses a line for you — tell me and I'll adapt.

## For Users

This project requires Claude Code authenticated on your machine (`claude` must be in PATH and logged in via `claude auth`).

You are responsible for your own compliance with [Anthropic's Consumer Terms](https://www.anthropic.com/legal/consumer-terms) and [Claude Code Legal and Compliance](https://code.claude.com/docs/en/legal-and-compliance).

This is not a substitute for the API. If you need guaranteed availability, SLAs, or commercial use — pay for API credits.
