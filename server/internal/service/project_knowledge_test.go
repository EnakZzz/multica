package service

import (
	"strings"
	"testing"
)

func TestKnowledgeSearchTermsAddsCJKBigrams(t *testing.T) {
	terms := knowledgeSearchTerms("中文经验库 修复路径")
	got := map[string]bool{}
	for _, term := range terms {
		got[term] = true
	}
	for _, want := range []string{"中文经验库", "中文", "文经", "经验", "验库", "修复路径", "修复", "复路", "路径"} {
		if !got[want] {
			t.Fatalf("knowledgeSearchTerms missing %q in %#v", want, terms)
		}
	}
}

func TestKnowledgeSearchTSQueryUsesOrPrefixTerms(t *testing.T) {
	query := knowledgeSearchTSQuery("CompleteTask 中文经验库")
	for _, want := range []string{"completetask:*", "中文:*", "经验:*"} {
		if !strings.Contains(query, want) {
			t.Fatalf("knowledgeSearchTSQuery missing %q in %q", want, query)
		}
	}
}
