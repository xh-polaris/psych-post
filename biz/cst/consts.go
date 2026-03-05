package cst

// mapper层字段枚举
const (
	Id             = "_id"
	ConversationID = "conversationId"
	MessageID      = "messageId"
	UserID         = "userId"
	CreateTime     = "createTime"
	UpdateTime     = "updateTime"
	DeleteTime     = "deleteTime"
	UnitID         = "unitId"
	Code           = "code"
	CodeType       = "codeType"

	// 预警管理-情绪类型
	Danger    = "danger"
	Depress   = "depress"
	Negative  = "negative"
	Normal    = "normal"
	Processed = "processed" // 预警状态-已处理
	Pending   = "pending"   // 预警状态-待处理

	Status        = "status"
	DeletedStatus = -1
	Meta          = "$meta"
	TextScore     = "textScore"
	Score         = "score"
	NE            = "$ne"
	LT            = "$lt"
	GT            = "$gt"
	In            = "$in"
	Set           = "$set"
	Text          = "$text"
	Search        = "$search"
	Regex         = "$regex"
	Options       = "$options"
)

const (
	// System is the role of a system, means the message is a system message.
	System     = "system"
	SystemEnum = 0
	// Assistant is the role of an assistant, means the message is returned by ChatModel.
	Assistant     = "assistant"
	AssistantEnum = 1
	// User is the role of a user, means the message is a user message.
	User     = "user"
	UserEnum = 2
	// Tool is the role of a tool, means the message is a tool call output.
	Tool     = "tool"
	ToolEnum = 3
)
