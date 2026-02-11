# 实现计划: LLM驱动的软件自助服务系统

## 概述

基于Go + Wails框架构建RAG驱动的软件自助服务平台。采用增量开发方式，从核心数据层开始，逐步构建业务逻辑层和前端界面，每个阶段都包含对应的测试任务。

## 任务

- [x] 1. 项目初始化与基础架构
  - [x] 1.1 初始化Wails项目并配置Go模块
    - 使用 `wails init` 创建项目骨架（Web模式）
    - 配置 go.mod，添加依赖：gopdf2, goword, goexcel, goppt, gopter, sqlite驱动
    - 创建目录结构：internal/parser, internal/chunker, internal/vectorstore, internal/query, internal/llm, internal/auth, internal/config, internal/pending
    - _Requirements: 8.1_

  - [x] 1.2 实现配置管理器（ConfigManager）
    - 创建 internal/config/config.go，定义 Config, LLMConfig, EmbeddingConfig, VectorConfig, OAuthConfig, AdminConfig 结构体
    - 实现 Load/Save/Get/Update 方法，API密钥使用AES加密存储
    - 实现配置文件默认值和验证逻辑
    - _Requirements: 10.1, 10.4, 10.5_

  - [ ]* 1.3 编写配置管理器属性测试
    - **Property 17: 配置序列化往返一致性**
    - **Property 18: API密钥加密存储**
    - **Property 20: 配置热更新生效**
    - **Validates: Requirements 10.1, 10.4, 10.5**

  - [x] 1.4 实现SQLite数据库初始化
    - 创建 internal/db/db.go，实现数据库连接和表创建
    - 创建 documents, chunks, pending_questions, users, sessions 表
    - 实现数据库迁移逻辑
    - _Requirements: 6.1, 6.4_

- [x] 2. 向量存储与文本处理核心
  - [x] 2.1 实现向量序列化工具
    - 创建 internal/vectorstore/serialize.go
    - 实现 SerializeVector 和 DeserializeVector 函数
    - 实现 CosineSimilarity 余弦相似度计算函数
    - _Requirements: 6.1_

  - [ ]* 2.2 编写向量序列化属性测试
    - **Property 14: 向量序列化往返一致性**
    - **Validates: Requirements 6.1**

  - [x] 2.3 实现 SQLiteVectorStore
    - 创建 internal/vectorstore/store.go，实现 VectorStore 接口
    - 实现 Store 方法：批量插入 Chunk 和 Embedding 到 chunks 表
    - 实现 Search 方法：加载所有向量计算余弦相似度，返回 Top-K 结果
    - 实现 DeleteByDocID 方法：按文档ID删除所有关联数据
    - _Requirements: 6.1, 6.2, 6.3, 6.4_

  - [ ]* 2.4 编写向量存储属性测试
    - **Property 3: 向量存储与检索的往返一致性**
    - **Property 4: 相似度搜索结果的排序与数量约束**
    - **Property 5: 文档删除的完整性**
    - **Validates: Requirements 6.1, 6.2, 6.3, 6.4, 4.2**

  - [x] 2.5 实现 TextChunker（文本分块器）
    - 创建 internal/chunker/chunker.go
    - 实现 Split 方法：按固定大小分块，支持重叠
    - 处理边界情况：文本短于 chunkSize、空文本等
    - _Requirements: 2.4_

  - [ ]* 2.6 编写文本分块器属性测试
    - **Property 1: 文本分块覆盖性与重叠正确性**
    - **Validates: Requirements 2.4**

- [x] 3. 检查点 - 确保所有测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 4. 文档解析与外部服务客户端
  - [x] 4.1 实现 Document Parser
    - 创建 internal/parser/parser.go
    - 实现 Parse 方法：根据文件类型分发到对应解析函数
    - 实现 parsePDF（gopdf2）、parseWord（goword）、parseExcel（goexcel）、parsePPT（goppt）
    - 实现文本清洗函数：移除多余空白和特殊字符
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_

  - [ ]* 4.2 编写文档解析器属性测试
    - **Property 2: 文件类型验证的完备性**
    - **Property 12: 文本清洗有效性**
    - **Property 13: 解析错误信息完整性**
    - **Validates: Requirements 2.1, 2.7, 5.5, 5.6**

  - [x] 4.3 实现 Embedding Service 客户端
    - 创建 internal/embedding/service.go
    - 实现 APIEmbeddingService，支持 OpenAI 兼容的 Embedding API
    - 实现 Embed 和 EmbedBatch 方法
    - _Requirements: 1.2, 2.5_

  - [x] 4.4 实现 LLM Service 客户端
    - 创建 internal/llm/service.go
    - 实现 APILLMService，支持 OpenAI 兼容的 Chat Completion API
    - 实现 Prompt 构造逻辑（系统提示词 + Chunk内容 + 用户问题）
    - 实现重试逻辑（失败重试一次）
    - _Requirements: 7.1, 7.2, 7.3, 7.4_

  - [ ]* 4.5 编写 LLM Service 属性测试
    - **Property 6: Prompt构造的完整性**
    - **Validates: Requirements 1.3, 7.2**

- [x] 5. 业务逻辑层
  - [x] 5.1 实现 Document Manager
    - 创建 internal/document/manager.go
    - 实现 UploadFile：验证文件类型 → 解析 → 分块 → 向量化 → 存储
    - 实现 UploadURL：抓取URL内容 → 解析 → 分块 → 向量化 → 存储
    - 实现 DeleteDocument 和 ListDocuments
    - 在 documents 表中记录文档元数据和处理状态
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 4.1, 4.2, 4.3_

  - [ ]* 5.2 编写文档管理属性测试
    - **Property 11: 文档列表完整性**
    - **Validates: Requirements 4.1**

  - [x] 5.3 实现 Query Engine
    - 创建 internal/query/engine.go
    - 实现 Query 方法：Embedding → 向量搜索 → 判断阈值 → LLM生成或创建Pending
    - 构造包含引用来源的 QueryResponse
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5_

  - [ ]* 5.4 编写查询引擎属性测试
    - **Property 7: 查询响应包含引用来源**
    - **Property 8: 低相似度查询创建待回答问题**
    - **Validates: Requirements 1.4, 1.5**

  - [x] 5.5 实现 Pending Question Manager
    - 创建 internal/pending/manager.go
    - 实现 CreatePending、ListPending、AnswerQuestion
    - AnswerQuestion：将回答内容分块存入知识库 → 调用LLM生成总结回答
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 1.7_

  - [ ]* 5.6 编写待回答问题管理属性测试
    - **Property 9: 管理员回答存入知识库**
    - **Property 10: 待回答问题列表排序**
    - **Validates: Requirements 3.3, 3.5**

- [x] 6. 检查点 - 确保所有测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 7. OAuth认证与会话管理
  - [x] 7.1 实现 OAuth Client
    - 创建 internal/auth/oauth.go
    - 实现 GetAuthURL 和 HandleCallback，支持 Google、Apple、Amazon、Facebook
    - 使用 golang.org/x/oauth2 库
    - _Requirements: 9.1, 9.2, 9.3, 9.4_

  - [x] 7.2 实现会话管理
    - 创建 internal/auth/session.go
    - 实现会话创建、验证、过期清理
    - 实现管理员密码验证（bcrypt哈希）
    - _Requirements: 9.5, 10.2, 10.3_

  - [ ]* 7.3 编写认证属性测试
    - **Property 15: OAuth认证URL生成**
    - **Property 16: 会话有效性验证**
    - **Property 19: 管理员密码验证**
    - **Validates: Requirements 9.2, 9.5, 10.3**

- [x] 8. Wails后端绑定与API层
  - [x] 8.1 实现 Wails 后端绑定
    - 创建 app.go，定义 App 结构体，注入所有服务组件
    - 绑定查询接口：App.Query(question string) → QueryResponse
    - 绑定文档管理接口：App.UploadFile, App.UploadURL, App.ListDocuments, App.DeleteDocument
    - 绑定待回答问题接口：App.ListPendingQuestions, App.AnswerQuestion
    - 绑定认证接口：App.GetOAuthURL, App.HandleOAuthCallback, App.AdminLogin
    - 绑定配置接口：App.GetConfig, App.UpdateConfig
    - _Requirements: 8.1_

- [x] 9. 前端界面实现
  - [x] 9.1 实现 OAuth 登录页面
    - 创建 frontend/src/pages/Login.vue（或 .jsx）
    - 显示 Google、Apple、Amazon、Facebook 登录按钮
    - 调用 Wails 绑定的 GetOAuthURL 和 HandleOAuthCallback
    - _Requirements: 9.1, 9.2, 9.3, 9.4_

  - [x] 9.2 实现 User_Chat 聊天界面
    - 创建 frontend/src/pages/Chat.vue
    - 实现聊天消息列表、输入框、发送按钮
    - 调用 Wails 绑定的 Query 接口
    - 显示加载状态、回答内容和引用来源
    - 显示 Pending 状态提示
    - 支持多轮对话历史显示
    - _Requirements: 1.1, 1.4, 1.5, 1.6, 8.2, 8.4_

  - [x] 9.3 实现 Admin_Panel 管理面板
    - 创建 frontend/src/pages/Admin.vue
    - 实现管理员密码登录界面
    - 实现文档管理页面：文件上传、URL提交、文档列表、删除操作
    - 实现待回答问题页面：问题列表、回答界面（文字/图片/URL）
    - 实现设置页面：API配置修改
    - _Requirements: 2.1, 2.2, 2.6, 3.1, 3.2, 3.5, 4.1, 4.3, 8.3, 10.2, 10.5_

  - [x] 9.4 实现响应式布局和路由
    - 配置前端路由：/login, /chat, /admin
    - 实现响应式CSS布局，适配桌面和平板
    - 实现导航和页面切换
    - _Requirements: 8.4, 8.5_

- [x] 10. 集成与最终检查点
  - [x] 10.1 集成所有组件并连接前后端
    - 在 main.go 中初始化所有服务组件并注入 App
    - 配置 Wails 应用启动参数（Web模式）
    - 确保前后端数据流通畅
    - _Requirements: 8.1_

  - [ ]* 10.2 编写集成测试
    - 测试完整的文档上传→解析→分块→向量化→存储流程
    - 测试完整的用户提问→检索→LLM回答流程
    - 测试待回答问题的完整生命周期
    - _Requirements: 1.1-1.7, 2.1-2.7, 3.1-3.5_

- [x] 11. 最终检查点 - 确保所有测试通过
  - 确保所有测试通过，如有问题请向用户确认。

## 备注

- 标记 `*` 的任务为可选任务，可跳过以加快MVP开发
- 每个任务引用了具体的需求编号以确保可追溯性
- 检查点任务确保增量验证
- 属性测试验证通用正确性属性，单元测试验证具体示例和边界情况
