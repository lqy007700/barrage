package filter

import (
	"bufio"
	"os"
	"strings"
)

// LoadWordsFromFile 从文件加载敏感词
func LoadWordsFromFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	words := make([]string, 0, 128)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		words = append(words, word)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return words, nil
}
