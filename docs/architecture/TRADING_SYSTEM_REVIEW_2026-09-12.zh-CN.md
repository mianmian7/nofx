# NOFX 交易系统专项改造

日期：2026-09-12。基于当前 `dev` 工作区；这是本地代码改造和隔离测试记录，没有进行生产发布，也没有启动交易、修改策略参数或重置任何账本。

## 判断

系统层最应优先保证的是：过期决策不能继续启动开仓；订单响应不确定时能查清实际结果；多个执行者的风险约束不能互相绕过；策略评价能解释真实成本与执行偏差。增加指标和调整阈值，应以这些边界可信为前提。

本文不依据未经独立复算的运行统计断言策略表现或修改风险阈值。本轮在已有开仓保护、Paper 风控和策略代码基础上作局部增量。

## 已落实

### 1. 把周期截止时间传到开仓边界

生产 `runCycle` 现在把 `requestCtx` 传入 decision batch。每笔决策开始前检查取消状态，过期会记录原因并停止剩余 batch；成功后的间隔等待也可被取消。

多头、空头的准备流程可能等待行情、仓位、余额查询，因此在设置保证金模式前，以及 `OpenLong/OpenShort` 调用前再次检查。Paper maker 和 taker 接收相同 context，获取行情或深度后，在账本锁内检查取消再创建挂单或成交。原有无 context 入口保留兼容。

订单已成功提交后，确认、止损止盈与失败补偿继续执行；补偿平仓沿用独立的保护期限。取消不能把已成交仓位留在无保护状态。

实现：`trader/auto_trader_loop.go`、`trader/auto_trader_orders.go`、`trader/paper_broker.go`。回归：`trader/decision_deadline_test.go`。

**边界**：这是 AutoTrader 调用适配器前的门禁。交易所适配器内部仍有不接收 context 的同步调用，例如 Binance 开仓使用 `Do(context.Background())`；若取消发生在适配器内部准备或提交期间，本轮不能撤销已开始的调用。下单响应丢失后的结果核对也仍需独立实现，不能把这些问题说成已修好。

### 2. 行情重连真正释放旧连接，闲置订阅自动回收

原 `RedundantStream` 在序列断档后重新调用 connector，但旧 connector 使用同一长期 context，其生产协程和连接可能仍存活。现在每次连接有独立 context，在替换连接前取消旧连接。新增幂等 `Stop`，立即撤回快照并取消两路来源，已停止实例不会重新发布晚到事件。CoinAnk 满通道发送也能响应取消。

当前使用的 OKX 价格/资金费率、Bitget 资金费率 registry 在最后一次请求后闲置 5 分钟自动移除实例与附属健康元数据；保留的 CoinAnk 诊断/旧展示代码同步接入这一生命周期。当前交易深度走交易所原生 REST，不走 CoinAnk，不能把后者的清理说成现有交易路径的连接收益。5 分钟是资源保留期，**不是行情新鲜度 TTL**；原 fresh/reconciled 判定保持不变。下一次请求创建新实例并重新取得有效快照。

回收使用 entry 对应的 timer，回调在 registry 锁内核对实例身份和最新需求时间；新请求与到期回调竞争时仍以最新需求为准。删除后在锁外 Stop，不依赖新的请求或健康轮询来触发最后一条闲置流的清理。晚到 REST 结果不再为已删除实例重建孤儿元数据。

实现：`market/stream_runtime.go`、`market/stream_retirement.go` 及三个 registry 文件。回归：`market/stream_lifecycle_test.go`。Stop 是取消通知，不是同步 join；fake connector 验证取消与重连边界，未测量线上 socket/goroutine 数量下降。

### 3. 实际终端盘口请求单飞与取消

当前路由使用 `TerminalDashboard → OrderBook/KlineChart`。旧 `AdvancedChart` 不在当前终端路径，本轮没有将优化它当作实际页面收益。

非 Binance 盘口维持 5 秒刷新节奏，但上次请求未完成时跳过新请求；切换交易对、交易所或卸载会取消旧 HTTP 请求，同时保留旧响应隔离。Axios 透传 AbortSignal，主动取消保留 cancellation 类型且不显示网络故障 toast。Binance 的初始 REST 深度请求同样可取消，WebSocket 更新逻辑不变。

实现：`web/src/components/terminal/OrderBook.tsx`、`web/src/lib/api/data.ts`、`web/src/lib/httpClient.ts`。测试使用 deferred promise 和真实 Axios interceptor 链（仅替换传输），验证 15 秒未完成时仅发一次请求、完成后继续刷新、取消与旧响应隔离。

## 下一阶段实施顺序

### P0：开仓结果未知状态与恢复对账

已确认：Binance、Bitget 开仓生成 client order ID 后，提交错误直接返回；只有成功返回 order map 才进入 AutoTrader 的 `recordAndConfirmOrder` 与开仓保护。若交易所接受订单而响应丢失，本地可能把它当失败，无法立刻按本次提交进入保护流程。后续仓位查询可以发现部分情况，但不足以构成幂等保障。

建议先覆盖实际使用的交易所：在发送前持久化订单意图、稳定 client ID、交易账户、symbol/side/数量与保护参数；状态至少区分待提交、已提交、结果未知、已确认和已拒绝。未知期间禁止相同意图的新开仓，但保留风险收缩和保护动作。用 client ID 查询订单，结合订单事件与仓位核对，确认成交后接续保护；进程重启也恢复这条状态链。一次“订单不存在”响应不能自动证明原请求绝未成交。

验收用隔离传输故障：POST 实际被接受但客户端超时，查询显示成交，系统只能产生一个订单意图并补上保护；持续无法确认时保持 unknown，不能重新生成 ID 重提；重启后仍能恢复。该项涉及持久化与多个执行阶段，应作为完整改造交付。

[Binance USD-M 官方约定](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info)明确区分执行状态未知与明确失败，要求前者先通过订单查询或 WebSocket 验证，避免重复提交。本轮尚未实施这一状态机。

### P1：账户级风险预留和组合敞口

已确认 `enforceOpenRiskBudget` 在单个 AutoTrader 内读取账户状态并计算，manager 没有共享的账户预算预留；检查通过与交易所写入之间存在并发窗口。Paper 则按 traderID 持久化独立账本，由各自 broker 锁保护。

先核对 trader 与实际资金账户的映射。对共享真实账户的多个 trader，应在同一账户范围原子预留开仓风险，计入已有仓位和未成交开仓单；订单明确拒绝或撤销后释放，成交后转为仓位风险，结果未知时保留预留，重启时对账恢复。不能因 HTTP 超时就释放预算。止损距离、数量、费用和滑点应共同形成风险金额，不能仅把保证金占比作为损失上限。独立 Paper 账本应分别约束，不能未经业务定义混成共享余额。

验收重点是并发：两个 trader 同时通过余额查询时，不能各自占满相同预算；未确认订单也不能被当作零风险。上限取值和组合情景由实际账户与策略复盘决定，本文不指定交易阈值。

### P1：把策略评价与执行成本连接起来

当前 Paper 已有总手续费、Maker/Taker、已平仓费用、Funding 明细以及按方向/持仓时长的净 PnL 展示，不需要重新做一套费用统计。真正的缺口是：PaperFill 没有参考行情价和滑点成本字段，也没有跨 decision 与 fill 的信号归因标识。配置的 SlippageBPS 已影响成交价，不能再当作额外费用重复扣除。

建议记录决策时行情、计划价格、提交时间、确认时间、实际成交价和运行策略版本。将毛收益、手续费、资金费与可测得的执行偏差分别展示；无法取得的真实成本要标明估计，不能与实测混合。保存实际生效的策略与提示词版本，复盘时才能知道每笔交易采用了什么规则。复用 close_reason 先按退出原因汇总净收益；没有建立决策与成交关联前，不把它命名为“信号收益”。

验收从少量历史订单逐笔勾稽：成交现金流与净权益一致，归因不重复扣费，改提示词或风险规则前后能按版本筛选。样本外与滚动窗口评价应包括成本和未成交情况，不能只按少量已平仓交易选择策略。

### P2：隔离账户探测与交易关键请求

`api/exchange_account_state.go` 的缓存 miss 仍会重复探测相同账户，外层 8 秒等待结束不代表内部 `GetBalance` 已取消。建议按用户/配置版本合并探测、在失效后拒绝旧结果回写，再逐个把总预算传到适配器读取；不同账户隔离。止损、紧急平仓和订单核对需要有可用的请求额度，不能被页面探测耗尽。

## 验证记录

- `go test -race -p 2 ./market ./trader -count=1`：两包完整测试及竞态检查通过（10.994 s / 18.627 s）。
- `npm test -- --maxWorkers=2`：31 个文件、188 个测试通过。
- TypeScript/Vite 生产构建通过；主 JS 1,514.53 kB（gzip 440.34 kB），本轮没有做分包优化，既有大 chunk 与 Browserslist 提示仍存在。
- 新增晚到 REST 元数据回归后，market 生命周期相关测试再次通过 race；`go vet -p 2 ./market ./trader` 通过。
- `go test -p 2 ./... -run '^$' -count=1`：全部后端包及测试代码编译通过；这不代表全部后端测试函数已执行。
- 本轮前端文件 ESLint 检查与格式修正通过；`git diff --check` 通过。
- 未运行生产 soak、真实交易所下单或对账故障注入；本轮没有证明线上时延、资源占用或交易收益改善。

第一轮账户隔离、高频查询、HTTP 关闭和 trader 缓存改造及基准，见 [项目分析记录](PROJECT_REVIEW_2026-09-12.zh-CN.md)。
