# NOFX Architecture Documentation

**Language:** [English](README.md) | [中文](README.zh-CN.md)

Technical documentation for developers who want to understand NOFX internals.

---

## Overview

NOFX is a full-stack AI trading platform for cryptocurrency and US stock markets:

- **Backend:** Go (Gin framework, SQLite)
- **Frontend:** React/TypeScript (Vite, TailwindCSS)
- **AI Models:** DeepSeek, Qwen, OpenAI (GPT-5.2), Claude, Gemini, Grok, Kimi
- **Exchanges:** Binance, Bybit, OKX, Hyperliquid, Aster, Lighter

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              NOFX Platform                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────┐  ┌─────────────────────────────────────┐│
│  │  Strategy   │  │         Live Trading                ││
│  │   Studio    │  │        (Auto Trader)                ││
│  └──────┬──────┘  └──────────────────┬──────────────────┘│
│         │                            │                   │
│         └────────────────────────────┘                   │
│                                    │                                        │
│                          ┌─────────▼─────────┐                              │
│                          │   Core Services   │                              │
│                          │  - Market Data    │                              │
│                          │  - AI Providers   │                              │
│                          │  - Risk Control   │                              │
│                          └─────────┬─────────┘                              │
│                                    │                                        │
│         ┌──────────────────────────┼──────────────────────────┐            │
│         │                          │                          │            │
│  ┌──────▼──────┐         ┌─────────▼─────────┐      ┌────────▼────────┐   │
│  │  Exchanges  │         │     Database      │      │   Frontend UI   │   │
│  │  (CEX/DEX)  │         │    (SQLite)       │      │   (React SPA)   │   │
│  └─────────────┘         └───────────────────┘      └─────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Module Documentation

### Core Modules

| Module | Description | Documentation |
|--------|-------------|---------------|
| **Strategy Studio** | Strategy configuration, coin selection, data assembly, AI prompts | [STRATEGY_MODULE.md](STRATEGY_MODULE.md) |

### Module Overview

#### Strategy Module
Complete strategy configuration system including:
- Coin source selection (static list, AI500 pool, OI ranking)
- Market data indicators (K-lines, EMA, MACD, RSI, ATR)
- Prompt construction (system prompt, user prompt, sections)
- AI response parsing and decision execution
- Risk control enforcement

**[Read Full Documentation →](STRATEGY_MODULE.md)**

#### Unified live market-data resilience

All trading and paper callers use the same fresh-only provider contract. A
caller cannot opt into stale K-lines or cached marks. Binance, OKX, and Bitget
provide the complete price/K-line/depth/funding/OI/contract capability set;
partial providers are rejected before a trader starts instead of failing in a
later decision cycle.

K-lines hedge the venue's native public API with a second fresh same-venue
source. Simultaneous results are checked for candle-time and close-price
agreement. Depth uses two persistent same-venue WebSocket sessions with a
fresh native REST request as bootstrap/recovery. A disconnect or sequence gap
makes that stream unusable until a new snapshot has reconciled it. The runtime
never returns an older successful response after an upstream failure.

Bitget funding, mark price, and index price use two supervised official ticker
WebSocket sessions as the primary source. If neither has a fresh event, the
provider makes at most two fresh REST attempts under a shared five-second
deadline; an older successful value is never a fallback. A candidate-symbol
failure is isolated only while at least 80 percent of the candidate universe
has complete fresh data. Below that coverage, the whole decision cycle fails
closed before the AI call. Any missing data for an open position always fails
the whole cycle closed.

OKX last price likewise uses two supervised official `tickers` WebSocket
sessions. Funding combines the official `funding-rate` and `mark-price`
channels and publishes only after both timestamped components are present.
REST fallback for both paths is limited by the same five-second total deadline
and two-second per-attempt deadline. Contract metadata remains authoritative
REST bootstrap data and therefore fails closed when it cannot be verified.

Binance public data and signed trading requests also share one process-wide,
bounded priority admission queue. Paper mark prices, order-book maintenance,
funding snapshots, circuit recovery probes, and signed order mutations receive
critical priority; analysis and UI requests remain normal priority. Admission
waiting is separate from the per-attempt HTTP deadline, so local queue pressure
cannot be misreported as a network outage.

Only explicit Binance throttling or access responses (`418`, `429`, `451`) may
open a process-wide cooldown, honoring `Retry-After` when provided. Admission
timeouts, DNS/TLS/proxy failures, upstream timeouts, and `5xx` responses use
bounded per-request retries and fresh-source failover; they must never open or
extend the global cooldown.

`GET /api/market-data/health` reports venue capabilities, selected K-line
transport and freshness, per-connection depth timestamps, sequence,
reconciliation, reconnect, and gap state, plus Bitget funding-stream
freshness, REST fallback counts, and last errors.
The endpoint also reports OKX price-stream and funding/mark-stream readiness.

---

## Project Structure

```
nofx/
├── main.go                    # Entry point
├── api/                       # HTTP API (Gin framework)
├── trader/                    # Trading execution layer
├── strategy/                  # Strategy engine
├── market/                    # Market data service
├── mcp/                       # AI model clients
├── store/                     # Database operations
├── auth/                      # JWT authentication
├── manager/                   # Multi-trader management
└── web/                       # React frontend
    ├── src/pages/             # Page components
    ├── src/components/        # Shared components
    └── src/lib/api.ts         # API client
```

---

## Core Dependencies

### Backend (Go)

| Package | Purpose |
|---------|---------|
| `gin-gonic/gin` | HTTP API framework |
| `adshao/go-binance` | Binance API client |
| `markcheno/go-talib` | Technical indicators |
| `golang-jwt/jwt` | JWT authentication |

### Frontend (React)

| Package | Purpose |
|---------|---------|
| `react` | UI framework |
| `recharts` | Charts and visualizations |
| `swr` | Data fetching |
| `zustand` | State management |
| `tailwindcss` | CSS framework |

---

## Quick Links

- [Strategy Module](STRATEGY_MODULE.md) - How strategies work
- [Getting Started](../getting-started/README.md) - Setup guide
- [FAQ](../faq/README.md) - Frequently asked questions

---

## For Developers

**Want to contribute?**
- Read the module documentation above
- Check [Open Issues](https://github.com/NoFxAiOS/nofx/issues)
- Join our community

**Repository:** https://github.com/NoFxAiOS/nofx

---

[← Back to Documentation](../README.md)
