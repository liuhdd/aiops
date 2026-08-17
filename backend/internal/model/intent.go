package model

import "time"

type IntentKind string

const (
	KindGroup  IntentKind = "GROUP"
	KindSystem IntentKind = "SYSTEM"
	KindKB     IntentKind = "KB"
	KindMCP    IntentKind = "MCP"
)

// SystemIntentCodes 内置 SYSTEM 意图 code 全集（seed 与校验共用）。
var SystemIntentCodes = []string{
	"ListAlerts", "InspectAlert", "AskRootCause", "AskRecommendation", "AskStatus", "Unknown",
}

func IsSystemCode(code string) bool {
	for _, c := range SystemIntentCodes {
		if c == code {
			return true
		}
	}
	return false
}

type IntentNode struct {
	ID           string     `json:"id"`
	ParentID     string     `json:"parent_id"`
	Name         string     `json:"name"`
	Kind         IntentKind `json:"kind"`
	Code         string     `json:"code"`
	BindTarget   string     `json:"bind_target"`
	RuleKeywords []string   `json:"rule_keywords"`
	Description  string     `json:"description"`
	Examples     []string   `json:"examples"`
	Enabled      bool       `json:"enabled"`
	Deleted      bool       `json:"deleted"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// IsLeaf kind != GROUP。
func (n IntentNode) IsLeaf() bool { return n.Kind != KindGroup }

// IntentTreeNode 递归组树节点（管理页渲染）。
type IntentTreeNode struct {
	IntentNode
	Children []IntentTreeNode `json:"children"`
}

// LeafSpec 识别视图（服务层派生，agent 消费）。
type LeafSpec struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Examples     []string `json:"examples"`
	RuleKeywords []string `json:"rule_keywords"`
	Path         string   `json:"path"`
}

// 留痕动作常量
const (
	AuditCreate  = "create"
	AuditUpdate  = "update"
	AuditDelete  = "delete"
	AuditEnable  = "enable"
	AuditDisable = "disable"
)

type IntentAudit struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"node_id"`
	Action     string    `json:"action"`
	Operator   string    `json:"operator"`
	BeforeJSON string    `json:"before_json,omitempty"`
	AfterJSON  string    `json:"after_json,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
