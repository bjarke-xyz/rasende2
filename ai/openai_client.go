package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

func (o *OpenAIClient) GenerateImage(ctx context.Context, siteName string, siteDescription string, articleTitle string) (string, error) {
	imgReq := openai.ImageRequest{
		Model: "dall-e-3",
		// Prompt:         fmt.Sprintf("Et billede der passer til en artikel på nyhedsmediet %s. %s. Artiklens overskrift er '%s'. Billedet skal passe til artiklen. Billedet bør IKKE ligne en artikel eller en avis, eller indeholde aviser. Billedet skal være passende til artiklen. Artiklen vises på en hjemmeside.", siteName, siteDescription, articleTitle),
		// Prompt:         fmt.Sprintf("Create a header image for an article titled '%v'. The article will be published in an online news media called '%v'", articleTitle, siteName),
		Prompt:         fmt.Sprintf("Create a header image for an article titled '%v', to be used on %v. %v. **Do not include any text, such as the newspaper name, article title, or any other wording, in the image.**", articleTitle, siteName, siteDescription),
		N:              1,
		Size:           "1024x1024",
		Style:          "vivid",
		ResponseFormat: "b64_json",
	}
	log.Printf("GenerateImage - site: %v, articleTitle: %v", siteName, articleTitle)
	imgResp, err := o.client.CreateImage(ctx, imgReq)
	if err != nil {
		return "", fmt.Errorf("error generating openai image: %w", err)
	}
	if len(imgResp.Data) == 0 {
		return "", fmt.Errorf("openai image returned 0 results")
	}
	url, err := o.uploadImage(ctx, imgResp.Data[0].B64JSON, articleTitle)
	if err != nil {
		return "", fmt.Errorf("error uploading img base64 to s3")
	}
	return url, nil
}

func (o *OpenAIClient) uploadImage(ctx context.Context, imgBase64Json string, articleTitle string) (string, error) {
	imgBytes, err := base64.StdEncoding.DecodeString(imgBase64Json)
	if err != nil {
		return "", fmt.Errorf("error decoding base64json: %w", err)
	}
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: o.appContext.Config.S3ImageUrl,
		}, nil
	})
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithEndpointResolverWithOptions(r2Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(o.appContext.Config.S3ImageAccessKeyId, o.appContext.Config.S3ImageSecretAccessKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load r2 config: %w", err)
	}

	client := s3.NewFromConfig(cfg)
	bucket := o.appContext.Config.S3ImageBucket

	hash := sha256.New()
	hash.Write([]byte(articleTitle))
	hashBytes := hash.Sum(nil)
	hashString := fmt.Sprintf("%x", hashBytes)
	key := "rasende2/articleimgs/" + hashString + ".png"
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   bytes.NewReader(imgBytes),
	})
	if err != nil {
		return "", fmt.Errorf("error uploading to s2: %w", err)
	}
	url := o.appContext.Config.S3ImagePublicBaseUrl + "/" + key
	return url, nil
}

func (o *OpenAIClient) GenerateArticleTitles(ctx context.Context, siteName string, siteDescription string, previousTitles []string, newTitlesCount int, temperature float32) (*openai.ChatCompletionStream, error) {
	if newTitlesCount > 10 {
		newTitlesCount = 10
	}
	previousTitlesStr := ""
	model := "gpt-4o-mini"
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return nil, fmt.Errorf("failed to get tiktoken encoding: %w", err)
	}
	var token []int
	previousTitlesCount := 0
	for _, prevTitle := range previousTitles {
		previousTitlesCount++
		tmpStr := previousTitlesStr + "\n" + prevTitle
		token = tkm.Encode(tmpStr, nil, nil)
		if len(token) > 3000 {
			break
		}
		previousTitlesStr = tmpStr
	}
	log.Printf("GenerateArticleTitles - site: %v, tokens: %v, previousTitles: %v", siteName, len(token), previousTitlesCount)
	req := openai.ChatCompletionRequest{
		Model:       model,
		Temperature: temperature,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Du er en journalist på mediet %v. %v. \nDu vil få stillet en række tidligere overskrifter til rådighed. Find på %v nye overskrifter, der minder om de overskrifter du får. De nye overskrifter må gerne være sjove eller humoristiske, eller være satiriske i forhold til nyhedsmediet, men de skal stadig være realistiske nok, til at man kunne tro, at de er ægte. Begynd hver overskrift på en ny linje. Start hver linje med et mellemrum (' '). Returner kun overskrifter, intet andet. Lav højest %v overskrifter.", siteName, siteDescription, newTitlesCount, newTitlesCount),
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

func (o *OpenAIClient) GenerateArticleContent(ctx context.Context, siteName string, siteDescription string, articleTitle string, temperature float32) (*openai.ChatCompletionStream, error) {
	log.Printf("GenerateArticleContent - site: %v, title: %v, temperature: %v", siteName, articleTitle, temperature)
	model := openai.GPT3Dot5Turbo
	req := openai.ChatCompletionRequest{
		Model:       model,
		Temperature: temperature,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Du er en journalist på mediet %v. %v. \nDu vil få en overskrift, og du skal skrive en artikel der passer til den overskrift. Artiklen må IKKE starte med overskriften!", siteName, siteDescription),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: articleTitle,
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
