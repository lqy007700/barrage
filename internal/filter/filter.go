package filter

// Result 表示敏感词过滤结果
type Result struct {
	// 原始文本
	Original string

	// 是否命中敏感词
	Hit bool

	// 命中的敏感词列表
	Words []string
}

// Filter 表示敏感词过滤器接口
type Filter interface {
	// Check 检查输入文本是否命中敏感词
	Check(text string) Result
}
