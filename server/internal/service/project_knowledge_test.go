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

func TestCanonicalWikiResultsPreferReviewedAndExcludeNonWiki(t *testing.T) {
	results := []KnowledgeSearchResult{
		{
			TargetType: KnowledgeTargetMemoryItem,
			Score:      0.99,
			MemoryItem: &MemoryItem{ID: "mem-1", Title: "Old memory"},
		},
		{
			TargetType: KnowledgeTargetWikiPage,
			Score:      0.95,
			WikiPage:   &WikiPage{ID: "wiki-archived", Slug: "old", Status: "archived", UpdatedAt: "2026-05-26T08:00:00Z"},
		},
		{
			TargetType: KnowledgeTargetWikiPage,
			Score:      0.98,
			WikiPage:   &WikiPage{ID: "wiki-draft", Slug: "draft", Status: "draft", UpdatedAt: "2026-05-26T09:00:00Z"},
		},
		{
			TargetType: KnowledgeTargetWikiPage,
			Score:      0.80,
			WikiPage:   &WikiPage{ID: "wiki-reviewed", Slug: "reviewed", Status: "reviewed", UpdatedAt: "2026-05-26T07:00:00Z"},
		},
	}

	got := canonicalWikiResultsFromSearch(results, 5)
	if len(got) != 2 {
		t.Fatalf("expected 2 canonical wiki results, got %d: %#v", len(got), got)
	}
	if got[0].WikiPage == nil || got[0].WikiPage.Slug != "reviewed" {
		t.Fatalf("expected reviewed wiki first, got %#v", got[0].WikiPage)
	}
	if got[1].WikiPage == nil || got[1].WikiPage.Slug != "draft" {
		t.Fatalf("expected draft wiki second, got %#v", got[1].WikiPage)
	}
}

func TestWikiPagesToSearchResultsSetsFallbackMetadata(t *testing.T) {
	pages := []WikiPage{
		{ID: "wiki-1", Slug: "overview", Title: "Overview", Body: "Canonical project overview.", Status: "reviewed"},
	}

	got := wikiPagesToSearchResults(pages)
	if len(got) != 1 {
		t.Fatalf("expected one fallback result, got %d", len(got))
	}
	if got[0].TargetType != KnowledgeTargetWikiPage {
		t.Fatalf("expected wiki target type, got %q", got[0].TargetType)
	}
	if got[0].MatchType != "canonical_fallback" {
		t.Fatalf("expected canonical_fallback match type, got %q", got[0].MatchType)
	}
	if got[0].WikiPage == nil || got[0].WikiPage.Slug != "overview" {
		t.Fatalf("expected overview wiki page, got %#v", got[0].WikiPage)
	}
	if !strings.Contains(got[0].Snippet, "Canonical project overview") {
		t.Fatalf("expected fallback snippet from wiki body, got %q", got[0].Snippet)
	}
}
