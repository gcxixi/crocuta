# ADR-0002：版本化、可解释的 Issue 分组

状态：Accepted

## 背景

Issue 聚合是错误系统的核心产品行为。JavaScript bundle 名、压缩函数、行列号和动态消息会随发布变化；Source Map 缺失或算法调整也可能让相同错误产生不同输入。若 grouping hash 随代码发布被原地修改，历史 Issue 会不可预测地拆分或合并。

## 决策

- 分组实现为项目内纯函数，输入 Canonical Error 与明确的算法/配置版本。
- 先尊重 SDK fingerprint，再使用 exception、in-app stack 和规范化 message 的稳定组件。
- 输出一个或多个 hash 以及 component tree，持久化“为何这样分组”。
- `grouping_hashes` 以项目、算法版本、hash 唯一并映射到 Issue。
- 算法升级创建新版本；先 shadow 计算差异，再显式决定仅影响新事件或执行受控 merge/split。

## 结果

分组行为可测试、可解释、可回滚，允许未来为不同语言注册规则。代价是需要保存版本和 component tree，并提供离线评估/迁移工具；这是防止生产 Issue 静默漂移所必需的复杂度。
