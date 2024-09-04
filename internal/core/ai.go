package core

import (
	"context"
	"io"
	"time"
)

type AiClient interface {
	GenerateImage(ctx context.Context, siteName string, siteDescription string, articleTitle string, translateTitle bool) (string, error)
	GenerateArticleTitlesList(ctx context.Context, siteName string, siteDescription string, previousTitles []string, newTitlesCount int, temperature float32) ([]string, error)
	GenerateArticleTitles(ctx context.Context, siteName string, siteDescription string, previousTitles []string, newTitlesCount int, temperature float32) (ChatCompletionStream, error)
	SelectBestArticleTitle(ctx context.Context, siteName string, siteDescription string, articleTitles []string) (string, error)
	GenerateArticleContentStr(ctx context.Context, siteName string, siteDescription string, articleTitle string, temperature float32) (string, error)
	GenerateArticleContent(ctx context.Context, siteName string, siteDescription string, articleTitle string, temperature float32) (ChatCompletionStream, error)
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
