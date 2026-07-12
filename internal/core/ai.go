package core

import (
	"context"
	"io"
	"time"
)

// AiClient generates the fake news. Every method takes the whole NewsSite rather
// than its name and description separately: the two always travelled together,
// and the site is also what says which language to write in — an English site
// must not be given a Danish article.
//
// The prompts themselves stay in English whatever the site. Only the language
// they are told to write in changes, which is one prompt to maintain instead of
// one per edition.
type AiClient interface {
	GenerateImage(ctx context.Context, site NewsSite, articleTitle string, translateTitle bool) (string, error)
	GenerateArticleTitlesList(ctx context.Context, site NewsSite, previousTitles []string, newTitlesCount int, temperature float32) ([]string, error)
	GenerateArticleTitles(ctx context.Context, site NewsSite, previousTitles []string, newTitlesCount int, temperature float32) (ChatCompletionStream, error)
	SelectBestArticleTitle(ctx context.Context, site NewsSite, articleTitles []string) (string, error)
	GenerateArticleContentStr(ctx context.Context, site NewsSite, articleTitle string, temperature float32) (string, error)
	GenerateArticleContent(ctx context.Context, site NewsSite, articleTitle string, temperature float32) (ChatCompletionStream, error)
}

type ChatCompletionStream interface {
	Recv() (ChatCompletionStreamResponse, error)
}
type ChatCompletionStreamResponse interface {
	Content() string
}

type fakeChatCompletionStream struct {
	contents []string
	index    int
}

func NewFakeChatCompletionStream(contents []string) ChatCompletionStream {
	return &fakeChatCompletionStream{
		contents: contents,
	}
}

func (f *fakeChatCompletionStream) Recv() (ChatCompletionStreamResponse, error) {
	if f.index > (len(f.contents) - 1) {
		return nil, io.EOF
	}
	index := f.index
	f.index++
	content := f.contents[index]
	time.Sleep(100 * time.Millisecond)
	return &chatCompletionStreamResponse{
		content: content,
	}, nil
}

type chatCompletionStreamResponse struct {
	content string
}

func NewChatCompletionStreamResponse(content string) ChatCompletionStreamResponse {
	return &chatCompletionStreamResponse{
		content: content,
	}
}

func (f *chatCompletionStreamResponse) Content() string {
	return f.content
}
