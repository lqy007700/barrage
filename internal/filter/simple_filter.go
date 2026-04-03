package filter

import "strings"

// SimpleFilter 是一个简单的本地敏感词检测实现
// 当前版本使用 strings.Contains 逐词匹配，适合作为第一版可扩展骨架
type SimpleFilter struct {
	// 敏感词列表
	words []string
}

// NewSimpleFilter 创建简单过滤器
func NewSimpleFilter(words []string) *SimpleFilter {
	return &SimpleFilter{
		words: words,
	}
}

// Check 检查文本是否命中敏感词
func (f *SimpleFilter) Check(text string) Result {
	result := Result{
		Original: text,
	}

	if f == nil || len(f.words) == 0 || text == "" {
		return result
	}

	hitWords := make([]string, 0)

	for _, word := range f.words {
		if word == "" {
			continue
		}

		if strings.Contains(text, word) {
			hitWords = append(hitWords, word)
		}
	}

	result.Hit = len(hitWords) > 0
	result.Words = hitWords

	return result
}
