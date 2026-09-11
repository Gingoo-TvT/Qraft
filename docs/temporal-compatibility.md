# 工作流升级与兼容性

Qraft 使用 Temporal 保存生成任务进度。后端的 Temporal Go SDK 版本由 [go.mod](../backend/go.mod) 锁定，服务端和 UI 镜像由 [docker-compose.yml](../docker-compose.yml) 的不可变 digest 锁定。升级时应一起检查这些来源，不使用文档中的历史运行结果代替当前验证。

## 普通升级

1. 保留当前镜像、数据库、对象存储和实例配置的可恢复副本。
2. 尽量等待活动任务结束，再停止旧 worker。
3. 使用新代码运行回放测试，检查其能否处理已有代表性历史。
4. 在隔离实例验证迁移、任务生成、停止/恢复和结果保存，再更新自己的服务。
5. 对仍在运行的旧任务，保留能够处理它们的 worker，直到完成或经过明确迁移。

```bash
go -C backend test ./internal/workflow/replay ./internal/workflow -count=1
go -C backend test ./cmd/worker -count=1
```

仓库回放夹具使用合成内容，部分历史的主机身份已净化。通过这些测试说明对应历史能够回放；还应检查自己部署中实际存在的历史类型。导出历史时不要把模型密钥、非公开题目或用户信息提交到仓库。

## 可选 Worker Versioning

`WORKER_USE_BUILD_ID_VERSIONING` 默认关闭。只有已经配置任务队列兼容规则并验证服务端与 SDK 组合时才启用，且必须提供不可变的 `WORKER_BUILD_ID`。新 worker 启动不表示所有旧执行都会自动迁移。

普通自部署无需开启此功能。保持单一匹配版本并等待活动任务完成，通常更容易维护。

## 修改工作流时

- 已记录的 Activity 顺序、计时器、子工作流和返回结果参与回放；不要直接改变旧分支的行为。
- 使用不可变启动参数和已有版本分支区分新能力，确保旧输入仍按原语义执行。
- 新的 Store payload 必须由对应 worker 识别；不支持的版本应在写数据前明确拒绝。
- 关闭 API 的新建入口，不等于取消已经运行的工作流。先请求停止并等待终态，或让兼容 worker 继续收尾。
- 独立的批量生成工作流应继续保留注册，以便已开始的批次完成。

代码中 `algoforge.*` 协议名和部分工作流名称是兼容标识，不随界面品牌修改。涉及数据库的升级另见 [迁移与恢复](runbooks/database-migrations.md)。
