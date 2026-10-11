# PROJECT.md — nofx

> 注意：按 `agents.md` 约定，不得在本文件中写入私人主机别名、IP、域名、部署路径、运行数据或凭据。

## 用途

NOFX 是开源的 AI 交易终端：每个 trader 以语言模型作为策略，持续循环「读取行情结构 → 决策 → 执行 → 记录推理」，由 Go 运行时对每笔订单施加模型无法覆盖的硬性风控限制。支持多模型、多交易所并行运行，并按实际收益在排行榜上对比。（摘自 README）

本仓库是个人 fork（`mianmian7/nofx`），在上游基础上做了大量自定义：Paper 账户执行风控、Jev 双脑风控/信号、本地多周期行情分析服务、MCP 模型请求调度（含 Grok）等（见近期提交记录）。

## 技术栈

- 后端：Go（`go.mod`：`module nofx`，`go 1.25.11`），SQLite（已启用 WAL）
- 前端：`web/`，React + TypeScript + Vite（`nofx-web`），测试用 Vitest，ESLint
- AI 模型层：DeepSeek、OpenAI、Claude、Qwen、Gemini、Grok、Kimi、MiniMax、Claw402 等
- 交易所：Binance、Bybit、OKX、Hyperliquid、Bitget、KuCoin、Gate、Aster、Lighter
- 部署：Docker / docker-compose（`docker-compose*.yml`、`docker/`、`nginx/`）、Railway 配置

## 构建、运行与测试（来自根目录 Makefile 与 web/package.json）

- `make test` / `make test-backend`：`go test -v ./...`
- `make test-frontend`：`cd web && npm run test`（`vitest run`）
- `make build`：`go build -o nofx`
- `make run`：`go run main.go`
- `make run-frontend`：`cd web && npm run dev`（Vite）
- 其他目标：`test-coverage`、`build-frontend`、`fmt`、`lint`、`clean`、`docker-build`、`docker-up`、`docker-down`、`docker-logs`、`deps`、`deps-update`、`deps-frontend`

## 主要目录

- `main.go`、`cli.go`、`cmd/`（`backtest-lab`、`e2e_builder_fee`）：入口与命令
- `api/`：API 服务（JWT 鉴权、加密凭据存储）
- `trader/`：交易运行时与执行风控（体量最大）
- `kernel/`：决策内核、风控规则、Jev 信号评估
- `market/`：行情数据流与 K 线调度
- `signals/`：本地多周期行情分析服务
- `store/`：持久化（SQLite）
- `mcp/`：AI 模型提供方与请求调度
- `provider/`、`manager/`、`backtest/`、`auth/`、`crypto/`、`security/`、`safe/`、`telegram/`、`logger/`、`hook/`、`wallet/`、`telemetry/`
- `web/`：前端交易终端
- `docs/`：文档（含 `Git工作流规范.md`、`LOCAL_MARKET_ANALYSIS.zh-CN.md`、`operations/`、`plans/` 等）
- `docker/`、`nginx/`、`railway/`、`scripts/`：部署与脚本
- `data/`：本地运行数据（勿重置，见 agents.md）

## 当前分支状态（2026-10-11）

- 当前分支：`dev`，工作区干净
- `dev` 已于 2026-10-11 推送到 `origin/dev`，HEAD = `6a33d48c`「feat(trader): 避开15m窗口末尾采集以防开仓证据立即过期」（与 `origin/dev` 一致）
- 本地 `dev` 未配置上游跟踪分支（`git status -sb` 仅显示 `## dev`）
- 其他本地分支：`fix/paper-entry-quality`（已快进合并入 dev）、`fix/paper-reset-atomicity`、`fix/paper-reset-atomicity-orch`、`codex/security-hardening`、`codex/strategy-risk-controls`、`codex-backup-*`

## 远程

- `origin`：https://github.com/mianmian7/nofx.git（个人 fork，推送目标）
- 上游：`NoFxAiOS/nofx`（GitHub，仅在 Orca 元数据中记录为 upstream；本地 Git 未配置 `upstream` remote）。不要向上游推送。

## 备注

- 工作约定见 `agents.md`：实盘下单、修改实盘参数、停止 trader、重置 Paper/Live 数据、部署等均需明确确认；凭据输出需脱敏。
- 开发约定见 `CONTRIBUTING.md`。
