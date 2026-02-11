# 需求文档

## 简介

本系统是一个基于LLM驱动的软件自助服务平台。用户可以通过Web界面提出关于软件使用的问题，系统从向量数据库中检索相关文档资料，并由LLM对检索结果进行总结整理后回复用户。管理员可以上传和管理软件使用说明文档（支持URL和多种文件格式）。系统使用Go + Wails框架构建，以Web方式部署和访问。

## 术语表

- **Helpdesk_System**: 基于LLM驱动的软件自助服务系统整体
- **Query_Engine**: 负责接收用户问题、检索向量数据库并调用LLM生成回答的核心引擎
- **Document_Manager**: 负责文档上传、解析、分块和向量化存储的管理模块
- **Vector_Store**: 向量数据库，用于存储文档的向量化表示并支持语义相似度搜索
- **LLM_Service**: 大语言模型服务接口，通过外部API调用LLM提供商（支持OpenAI兼容接口），负责对检索到的文档片段进行总结整理并生成回答
- **Embedding_Service**: 向量嵌入服务接口，通过外部API调用嵌入模型提供商，负责将文本转换为向量表示
- **Document_Parser**: 文档解析器，使用vantagedatachat组织的开源库（gopdf2, goexcel, goppt, goword）将不同格式的文件转换为纯文本
- **Chunk**: 文档被分割后的文本片段，作为向量化和检索的基本单位
- **Embedding**: 文本的向量化表示，由Embedding_Service通过API生成，用于语义相似度计算
- **Admin_Panel**: 管理员操作界面，用于文档管理、问题回答和系统配置
- **User_Chat**: 用户聊天界面，用于提问和查看回答
- **Pending_Question**: 待回答问题，指系统无法从知识库中找到满意答案的用户问题，需要管理员人工回答
- **Admin_Answer**: 管理员对待回答问题的人工回复，可包含文字、图片或URL，回复内容会被存入知识库
- **OAuth_Provider**: 第三方OAuth身份认证提供商，支持Google、Apple、Amazon、Facebook等

## 需求

### 需求 1：用户提问与智能回答

**用户故事：** 作为一名软件用户，我希望通过Web界面提出关于软件使用的问题，以便快速获得准确的解答。

#### 验收标准

1. WHEN 用户在User_Chat界面输入问题并提交, THE Query_Engine SHALL 接收该问题并在30秒内返回回答
2. WHEN Query_Engine接收到用户问题, THE Query_Engine SHALL 调用Embedding_Service API将问题转换为Embedding并在Vector_Store中检索语义最相关的Chunk（Top-K，默认K=5）
3. WHEN Query_Engine检索到相关Chunk, THE LLM_Service SHALL 基于检索到的Chunk内容和用户问题生成总结性回答
4. WHEN LLM_Service生成回答, THE Helpdesk_System SHALL 在回答中附带引用来源信息（文档名称和相关片段位置）
5. IF Vector_Store中未检索到相关Chunk（相似度低于阈值）, THEN THE Helpdesk_System SHALL 将该问题标记为Pending_Question（关联当前登录用户）并通知用户"该问题已转交人工处理，请稍后查看回复"
6. WHEN 用户提交问题, THE User_Chat SHALL 显示加载状态指示器直到回答返回
7. WHEN Pending_Question被管理员回答后, THE Query_Engine SHALL 使用LLM_Service基于Admin_Answer内容生成总结性回答并推送给用户

### 需求 2：文档上传与管理

**用户故事：** 作为一名软件管理员，我希望能够上传和管理软件使用说明文档，以便系统拥有足够的知识库来回答用户问题。

#### 验收标准

1. WHEN 管理员在Admin_Panel上传文件, THE Document_Manager SHALL 接受PDF、Word、Excel、PPT格式的文件
2. WHEN 管理员在Admin_Panel提交文档URL, THE Document_Manager SHALL 抓取该URL的内容并进行处理
3. WHEN Document_Manager接收到文件, THE Document_Parser SHALL 使用对应的解析库（gopdf2解析PDF、goword解析Word、goexcel解析Excel、goppt解析PPT）将文件转换为纯文本
4. WHEN Document_Parser完成文本提取, THE Document_Manager SHALL 将文本按照固定大小（默认512个字符，128个字符重叠）分割为Chunk
5. WHEN Chunk生成完成, THE Document_Manager SHALL 调用Embedding_Service API将每个Chunk转换为Embedding并存储到Vector_Store中
6. WHEN 文档处理完成, THE Admin_Panel SHALL 显示文档处理状态（成功或失败及原因）
7. IF 上传的文件格式不在支持列表中, THEN THE Document_Manager SHALL 拒绝该文件并返回"不支持的文件格式"错误信息

### 需求 3：待回答问题管理

**用户故事：** 作为一名软件管理员，我希望能够查看和回答系统无法自动解答的用户问题，以便确保每个用户都能获得帮助。

#### 验收标准

1. WHEN 一个问题被标记为Pending_Question, THE Admin_Panel SHALL 在待回答列表中显示该问题，包含问题内容、提问时间和提问用户标识
2. WHEN 管理员选择回答一个Pending_Question, THE Admin_Panel SHALL 提供输入界面支持文字、图片上传和URL三种回答方式
3. WHEN 管理员提交Admin_Answer, THE Document_Manager SHALL 将回答内容（文字、图片描述文本、URL抓取内容）转换为Chunk并存储到Vector_Store中
4. WHEN Admin_Answer存储完成, THE Helpdesk_System SHALL 使用LLM_Service基于Admin_Answer内容生成总结性回答并推送给提问用户
5. WHEN 管理员查看待回答列表, THE Admin_Panel SHALL 按提问时间倒序排列，并区分显示"待回答"和"已回答"状态

### 需求 4：文档列表与删除

**用户故事：** 作为一名软件管理员，我希望能够查看和删除已上传的文档，以便维护知识库的准确性和时效性。

#### 验收标准

1. WHEN 管理员访问Admin_Panel的文档管理页面, THE Helpdesk_System SHALL 显示所有已上传文档的列表，包含文档名称、上传时间、文件类型和处理状态
2. WHEN 管理员选择删除某个文档, THE Document_Manager SHALL 从Vector_Store中移除该文档对应的所有Chunk和Embedding
3. WHEN 文档删除完成, THE Admin_Panel SHALL 更新文档列表并显示删除成功提示

### 需求 5：文档解析与文本提取

**用户故事：** 作为系统开发者，我希望系统能够准确解析多种格式的文档，以便提取出高质量的文本内容用于知识检索。

#### 验收标准

1. WHEN Document_Parser解析PDF文件, THE Document_Parser SHALL 使用gopdf2库提取文本内容，保留段落结构
2. WHEN Document_Parser解析Word文件, THE Document_Parser SHALL 使用goword库提取文本内容，保留标题和段落层次
3. WHEN Document_Parser解析Excel文件, THE Document_Parser SHALL 使用goexcel库逐Sheet提取单元格内容，以"Sheet名-行列"格式组织文本
4. WHEN Document_Parser解析PPT文件, THE Document_Parser SHALL 使用goppt库逐页提取幻灯片文本内容
5. IF Document_Parser解析文件时发生错误, THEN THE Document_Parser SHALL 返回包含文件名和错误原因的详细错误信息
6. THE Document_Parser SHALL 对提取的文本进行清洗，移除多余空白字符和无意义的特殊字符

### 需求 6：向量存储与检索

**用户故事：** 作为系统开发者，我希望系统能够高效地存储和检索文档向量，以便快速找到与用户问题最相关的文档片段。

#### 验收标准

1. THE Vector_Store SHALL 支持存储Embedding向量及其关联的Chunk元数据（文档ID、文档名称、片段位置）
2. WHEN 执行相似度搜索, THE Vector_Store SHALL 基于余弦相似度返回Top-K个最相关的Chunk
3. WHEN 删除文档, THE Vector_Store SHALL 根据文档ID删除该文档关联的所有Embedding和Chunk数据
4. THE Vector_Store SHALL 对Embedding数据进行持久化存储，确保系统重启后数据不丢失

### 需求 7：LLM集成与回答生成

**用户故事：** 作为系统开发者，我希望系统能够灵活集成LLM服务，以便生成高质量的回答。

#### 验收标准

1. THE LLM_Service SHALL 通过可配置的API端点与LLM提供商通信（支持OpenAI兼容接口）
2. WHEN 生成回答, THE LLM_Service SHALL 构造包含系统提示词、检索到的Chunk内容和用户问题的Prompt发送给LLM
3. IF LLM_Service调用LLM接口失败, THEN THE LLM_Service SHALL 重试一次，若仍失败则返回"服务暂时不可用，请稍后重试"的错误信息
4. THE LLM_Service SHALL 支持配置LLM的模型名称、温度参数和最大Token数

### 需求 8：Web界面与用户体验

**用户故事：** 作为一名用户，我希望系统提供简洁易用的Web界面，以便方便地使用自助服务功能。

#### 验收标准

1. THE Helpdesk_System SHALL 使用Wails框架以Web模式提供前端界面
2. THE User_Chat SHALL 提供聊天式交互界面，支持多轮对话历史显示
3. THE Admin_Panel SHALL 提供独立的管理入口，与用户聊天界面分离
4. WHEN 用户访问系统首页, THE Helpdesk_System SHALL 显示User_Chat界面作为默认页面
5. THE Helpdesk_System SHALL 支持响应式布局，适配桌面和平板设备的屏幕尺寸

### 需求 9：用户身份认证

**用户故事：** 作为一名用户，我希望通过第三方OAuth账号登录系统，以便安全便捷地使用自助服务功能。

#### 验收标准

1. WHEN 用户访问Helpdesk_System, THE Helpdesk_System SHALL 显示OAuth登录页面，提供Google、Apple、Amazon、Facebook登录选项
2. WHEN 用户选择某个OAuth_Provider进行登录, THE Helpdesk_System SHALL 重定向用户到该OAuth_Provider的授权页面
3. WHEN OAuth_Provider授权成功并回调, THE Helpdesk_System SHALL 创建或更新用户会话并跳转到User_Chat界面
4. IF OAuth_Provider授权失败或用户取消授权, THEN THE Helpdesk_System SHALL 显示登录失败提示并返回登录页面
5. WHEN 用户已登录, THE Helpdesk_System SHALL 在会话有效期内保持登录状态，无需重复认证
6. THE Helpdesk_System SHALL 支持通过配置文件设置各OAuth_Provider的Client ID和Client Secret

### 需求 10：系统配置与安全

**用户故事：** 作为一名软件管理员，我希望能够配置系统参数并确保管理功能的安全性，以便系统稳定可靠地运行。

#### 验收标准

1. THE Helpdesk_System SHALL 通过配置文件支持以下参数的配置：LLM API端点、LLM API密钥、LLM模型名称、Embedding API端点、Embedding API密钥、Embedding模型名称、向量数据库路径、Chunk大小、重叠大小、Top-K值
2. WHEN 管理员访问Admin_Panel, THE Helpdesk_System SHALL 要求输入管理员密码进行身份验证
3. IF 管理员密码验证失败, THEN THE Helpdesk_System SHALL 拒绝访问Admin_Panel并显示"密码错误"提示
4. THE Helpdesk_System SHALL 在配置文件中以安全方式存储API密钥（不以明文形式存储）
5. WHEN 管理员在Admin_Panel的设置页面修改API配置, THE Helpdesk_System SHALL 保存配置并立即生效，无需重启系统
