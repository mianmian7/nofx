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

## 影子评估（shadow-eval，已实现，分支 `feat/shadow-eval`）

- 目的：只记录、只统计，不改变任何交易逻辑。每个 trader 的每个 AI 决策（含 wait/hold、被风控拒绝的开仓、被 Jev 反抖动过滤器否决的信号）都写入新表 `shadow_decisions`，后台按公开 1m K 线回填 +15m/+1h/+4h/+24h 前瞻收益。
- 字段：trader、exchange、model（provider:模型名）、prompt_version（`system_prompt_template@strategy_id`）、prompt_hash（system prompt 的 sha256 前 12 位）、timeframe（扫描周期）、symbol、action/direction（long/short/flat/close）、source（ai/jev）、status（executed/rejected/vetoed/wait/hold）、confidence、probability（Jev）、regime（Jev 文本中的 regime 标签）、executed、决策时间、入场参考价（决策后下一根 1m 的开盘价）、各周期收益（按方向取符号）、BTC 前 24h 涨跌。
- 成本假设：往返 0.07%（maker 2bps + taker 5bps）；报表中 `net = 方向收益 − 0.07%`。
- 代码：`shadoweval/`（迁移、解析、摄取、回填、报表）、`main.go` 启动、`api/handler_shadow_eval.go`（`GET /api/shadow-eval/report`，需登录，只读）、`cmd/shadow-report`（只读打开 SQLite 的 CLI）。
- 开关：`NOFX_SHADOW_EVAL=off` 关闭；`NOFX_SHADOW_EVAL_SINCE`（RFC3339）调整首次回填起点，默认 2026-10-07；`NOFX_SHADOW_KLINE_URL` 覆盖行情源（默认 Binance USD-M 公共接口）。
- 已知局限：所有交易所统一用 Binance 永续价格近似；Binance 无此合约的记录标记 `unavailable`；同一信号在连续周期重复出现，样本存在自相关，bootstrap 区间会偏窄，判断优势时应按「标的+15m 桶」去重或使用块 bootstrap。

## 长期策略重构设计（仅设计，未实现）

### 背景与结论（2026-10-07 至 10-12 模拟盘）

255 笔平仓：胜率 27.8%，毛利 +1.55U，手续费 9.90U，净 −8.35U；94% 空单；74% 止损出场，中位止损 0.70% 对比 15m ATR 0.41%。信号方向的 1h/4h 前瞻收益与 0 无显著差异；AI confidence 与盈亏相关系数约 0.03；Jev 概率不校准（0.70 档反而最差），0.75 门槛从未触发。空单偏向来自「1h/4h EMA 同向」入场门槛叠加下跌行情，而非 prompt 本身。结论：问题在于**入场无可证明的优势 + 15m 级别噪声中的紧止损 + 成本占比过高**，而非某个参数。

### 设计原则

1. 先证明优势，再允许交易：任何 trader/prompt/模型组合必须先在影子数据中达标，才能进入 Paper。
2. AI 只做它可能擅长的事：判断方向与市场状态，不再输出止损、止盈和仓位。
3. 风险由确定性规则负责：止损、止盈、仓位、频率全部由代码按 ATR 与账户风险计算，可回测、可复现。
4. 置信度必须经过校准才可使用，否则仅作记录。
5. 拉长周期，降低交易频率，让单笔期望显著高于往返成本。

### 目标架构

- **决策周期**：1h 收盘触发（可选 4h 确认）。AI 输出结构化 JSON：`{symbol, direction: long|short|flat, regime: trend_up|trend_down|range|high_vol, raw_confidence: 0-100, thesis}`；不含价格字段，出现即视为无效。
- **规则化止损**：入场后止损 = `k × ATR(14, 1h)`（k 初值 1.5，按标的回测确定范围 1.2–2.0），并保留 `max(…, 0.5%)` 的下限。
- **止盈**：`TP ≥ 2R`（R = 止损距离）；1R 后止损移至保本（可选）；超过 N 根 1h K 线（初值 24）仍未触及则按时间平仓。
- **风险定仓**：单笔风险 = 权益 × r（Paper 初值 0.5%），`数量 = 风险金额 / 止损距离`，并受杠杆和最小名义金额约束；同方向总风险上限 2%，同一标的只允许一个持仓。
- **成本门槛**：`预期毛利 ≥ 5 × 往返成本`，否则不开仓。
- **频率**：每个 trader 每个标的每 4h 最多一次新开仓；日亏损达到 2R×3 后当天停止开仓（熔断）。
- **校准**：每周用影子数据为每个（模型 × prompt_hash × 周期）拟合 isotonic 回归，把 raw_confidence 映射为 P(hit@4h)。只有校准后概率 ≥ 阈值，且该组合整体期望 > 0 时才放行；校准表带版本号写库，决策记录引用版本。
- **晋级门槛（promotion gate）**：影子样本 n ≥ 200（按「标的+1h 桶」去重后）、4h 净期望 > 0 且块 bootstrap 95% 区间下限 > 0、按周分段至少 3/4 周为正、对 BTC 同期涨跌做 beta 中性化后仍为正。满足后才能进入 Paper；Paper 再跑 n ≥ 100 笔、净期望 > 0 才考虑实盘（实盘须另行明确授权）。不达标或连续 2 周转负则降级回影子。

### 实验范围

- **股票类永续 SNDK/SKHY**：上期样本中毛利为正的少数切片（各约 20 笔，区间跨 0，需复核）。设计为独立实验组：仅 1h 周期，long/short 均允许，交易时段与美/韩股开盘时段对齐做对照，单独统计。
- **加密主流（BTC/ETH/SOL）**：作为对照组。
- **Jev**：只保留做空方向与 regime 输出进入影子评估；做多信号在全部概率档均为负，暂不参与晋级。

### A/B 框架

- 新增「实验」实体：`experiment_id`、arms（每个 arm = 模型 + prompt 版本 + 参数集）、分配方式（同一时刻同一输入并行请求各 arm，避免时间混杂）、主指标（4h 净期望）、次指标（命中率、校准误差 ECE、MFE/MAE、回撤）。
- prompt 以内容哈希管理版本，变更自动产生新 arm，旧 arm 的影子数据不混用。
- 统计：arm 间差异用配对（同一决策时刻）bootstrap；预先设定样本量与停止规则，禁止看数据中途停（或使用序贯检验）；多重比较用 Holm 校正。

### 分阶段计划

| 阶段 | 内容 | 退出标准 |
|---|---|---|
| P0（已完成） | 影子评估上线，回填 10-07 起全部决策 | 表持续写入、报表可用 |
| P1（1–2 周） | 新增 1h 决策 prompt（只输出方向+regime）以影子方式并行运行，不下单；去重与块 bootstrap 进入报表；BTC beta 中性化指标 | 每个组合积累 n ≥ 200 |
| P2 | 规则化风控模块（ATR 止损、≥2R、风险定仓、成本门槛、频率与熔断），用影子信号 + K 线做离线回测 | 回测净期望 > 0，最大回撤 < 10R |
| P3 | isotonic 校准与晋级门槛自动化（每周任务，结果写库，报表展示） | 校准后 ECE < 5% |
| P4 | 达标组合进入 Paper（SNDK/SKHY 实验组 + 加密对照组）；A/B 框架上线 | Paper n ≥ 100，净期望 > 0 |
| P5 | 评估是否申请实盘小额（须单独授权） | — |

### 涉及的代码模块

- `kernel/engine_prompt.go`、`kernel/prompt_builder.go`、`kernel/schema.go`：新的 1h 方向/状态 prompt 与输出 schema（去掉价格字段）。
- `kernel/paper_entry_guard.go`、`kernel/deterministic_risk.go`：改为规则化止损/止盈/定仓与成本门槛；EMA 同向门槛降级为 regime 特征，不再单独决定方向。
- `kernel/jev_feature.go`、`kernel/dual_brain.go`：Jev 概率改走校准表；bearish_only 等偏向输出改为特征，不直接否决。
- `trader/`：按 1h 收盘调度、频率限制、日熔断、时间止损。
- `shadoweval/`：去重、块 bootstrap、beta 中性化、isotonic 校准、晋级门槛、实验/arm 维度。
- `store/`：新表 `calibration_models`、`experiments`、`experiment_arms`、`promotion_status`（只新增，不改旧表语义）。
- `api/`、`web/`：影子评估与实验面板（只读）。

### 成功标准

- 系统层面：连续 4 周 Paper 净期望 > 0，95% 区间下限 > 0，手续费占毛利 < 30%。
- 校准层面：校准后概率与实际命中率偏差（ECE）< 5%，高概率档命中率单调上升。
- 风险层面：止损出场占比 < 55%，单日最大亏损 ≤ 3R，最大回撤 ≤ 10R。
