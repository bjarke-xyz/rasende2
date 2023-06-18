package ai

import (
	"context"
	"fmt"
	"log"

	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/pkoukk/tiktoken-go"
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
	previousTitlesStr := ""
	model := openai.GPT3Dot5Turbo16K
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return nil, fmt.Errorf("failed to get tiktoken encoding")
	}
	var token []int
	for _, prevTitle := range previousTitles {
		tmpStr := previousTitlesStr + "\n" + prevTitle
		token = tkm.Encode(tmpStr, nil, nil)
		if len(token) > 14000 {
			break
		}
		previousTitlesStr = tmpStr
	}
	log.Printf("token count for site %v: %v", siteName, len(token))
	req := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Du er en journalist på mediet %v. Du vil få stillet en række tidligere overskrifter til rådighed. Find på %v nye overskrifter, der minder om de overskrifter du får. Begynd hver overskrift på en ny linje. Start hver linje med et mellemrum (' '). Returner kun overskrifter, intet andet. Lav højest %v overskrifter.", siteName, newTitlesCount, newTitlesCount),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: previousTitlesStr,
			},
		},
		Stream: true,
	}
	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}
	return stream, err
}
