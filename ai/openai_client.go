package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/bjarke-xyz/rasende2-api/pkg"
	openai "github.com/sashabaranov/go-openai"
)

type OpenAIClient struct {
	appContext *pkg.AppContext
	client     *openai.Client
}

func NewOpenAIClient(appContext *pkg.AppContext) *OpenAIClient {
	client := openai.NewClient(appContext.Config.OpenAIAPIKey)
	return &OpenAIClient{
		appContext: appContext,
		client:     client,
	}
}

func (o *OpenAIClient) GenerateArticleTitles(ctx context.Context, siteName string, previousTitles []string, newTitlesCount int) (*openai.ChatCompletionStream, error) {
	if newTitlesCount > 10 {
		newTitlesCount = 10
	}
	previousTitlesStr := strings.Join(previousTitles, "\n")
	req := openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Du er en journalist på mediet %v. Du vil få stillet en række tidligere overskrifter til rådighed. Find på %v nye overskrifter, der minder om de overskrifter du får. Begynd hver overskrift på en ny linje. Start hver linje med tegnet '-'", siteName, newTitlesCount),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: previousTitlesStr,
			},
		},
	}
	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}
	return stream, err
}
