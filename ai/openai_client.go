package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

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

const chatModel = "gpt-4o-mini"

func NewOpenAIClient(appContext *pkg.AppContext) *OpenAIClient {
	client := openai.NewClient(appContext.Config.OpenAIAPIKey)
	return &OpenAIClient{
		appContext: appContext,
		client:     client,
	}
}

func (o *OpenAIClient) GenerateImage(ctx context.Context, siteName string, siteDescription string, articleTitle string, translateTitle bool) (string, error) {
	if translateTitle {
		req := openai.ChatCompletionRequest{
			Model:       chatModel,
			Temperature: 1,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "Translate the following danish text to english",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: articleTitle,
				},
			},
			Stream: false,
		}
		translateResp, err := o.client.CreateChatCompletion(ctx, req)
		if err != nil {
			log.Printf("translate failed: %v", err)
		} else {
			if len(translateResp.Choices) > 0 {
				translatedTitle := translateResp.Choices[0].Message.Content
				if translatedTitle != "" {
					articleTitle = translatedTitle
				}
			}
		}
	}
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
	log.Printf("GenerateImage - Prompt=%v", imgReq.Prompt)
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

func (o *OpenAIClient) GenerateArticleTitlesList(ctx context.Context, siteName string, siteDescription string, previousTitles []string, newTitlesCount int, temperature float32) ([]string, error) {
	streamResp, err := o.GenerateArticleTitles(ctx, siteName, siteDescription, previousTitles, newTitlesCount, temperature)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	titlesArr := []string{}
	for {
		response, err := streamResp.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				titlesStr := sb.String()
				titles := strings.Split(titlesStr, "\n")
				for _, title := range titles {
					title := strings.TrimSpace(title)
					if len(title) > 0 {
						titlesArr = append(titlesArr, title)
					}
				}
				return titlesArr, nil
			} else {
				return titlesArr, err
			}
		}
		sb.WriteString(response.Choices[0].Delta.Content)
	}
}

func (o *OpenAIClient) GenerateArticleTitles(ctx context.Context, siteName string, siteDescription string, previousTitles []string, newTitlesCount int, temperature float32) (*openai.ChatCompletionStream, error) {
	previousTitlesStr := ""
	tkm, err := tiktoken.EncodingForModel(chatModel)
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
	// sysPrompt := fmt.Sprintf("Du er en journalist på mediet %v. %v. \nDu vil få stillet en række tidligere overskrifter til rådighed. Find på %v nye overskrifter, der minder om de overskrifter du får. De nye overskrifter må gerne være sjove eller humoristiske, eller være satiriske i forhold til nyhedsmediet, men de skal stadig være realistiske nok, til at man kunne tro, at de er ægte. Begynd hver overskrift på en ny linje. Start hver linje med et mellemrum (' '). Returner kun overskrifter, intet andet. Lav højest %v overskrifter.", siteName, siteDescription, newTitlesCount, newTitlesCount)
	sysPrompt := fmt.Sprintf("You are a journalist on a satirical news media like The Onion or Rokoko Posten. You must come up with new article titles, in the style of the news media '%v', but they must be fun and satirical so they can get published in The Onion or Rokoko Posten. You will be provided a description of the news media '%v', and a list of previous article titles from that news media. Start each title on a new line. Start each line with a space (' '). Return only titles, nothing else. Make at most %v titles.", siteName, siteName, newTitlesCount)
	log.Printf("GenerateArticleTitles - site: %v, tokens: %v, previousTitles: %v", siteName, len(token), previousTitlesCount)
	req := openai.ChatCompletionRequest{
		Model:       chatModel,
		Temperature: temperature,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: sysPrompt,
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Description of '%v': '%v'", siteName, siteDescription),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: previousTitlesStr,
			},
		},
		Stream: true,
	}
	log.Printf("GenerateArticleTitles - Prompts=%+v", req.Messages)
	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}
	return stream, err
}

func (o *OpenAIClient) SelectBestArticleTitle(ctx context.Context, siteName string, siteDescription string, articleTitles []string) (string, error) {
	log.Printf("SelectBestArticleTitle - site: %v", siteName)
	// sysPrompt := fmt.Sprintf("You are the editor of a news media called '%v'. %v. \n You are given %v news article titles. You must pick the one title which is most likely to get the most clicks. **RETURN ONLY THE TITLE, NOTHING ELSE**", siteName, siteDescription, len(articleTitles))
	sysPrompt := fmt.Sprintf("You are the editor of a satirical news media like the Onion or Rokoko Posten. You are given %v news articles titles. You must pick the one title which is most likely to get the most clicks. **RETURN ONLY THE TITLE, NOTHING ELSE**", len(articleTitles))
	req := openai.ChatCompletionRequest{
		Model:       chatModel,
		Temperature: 1,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: sysPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: strings.Join(articleTitles, "\n"),
			},
		},
		Stream: true,
	}
	log.Printf("SelectBestArticleTitle - Prompts=%+v", req.Messages)
	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI API error: %w", err)
	}
	var sb strings.Builder
	for {
		response, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				selectedTitle := sb.String()
				return selectedTitle, nil
			} else {
				return "", err
			}
		}
		sb.WriteString(response.Choices[0].Delta.Content)
	}
}

func (o *OpenAIClient) GenerateArticleContentStr(ctx context.Context, siteName string, siteDescription string, articleTitle string, temperature float32) (string, error) {
	streamResp, err := o.GenerateArticleContent(ctx, siteName, siteDescription, articleTitle, temperature)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for {
		response, err := streamResp.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				articleContent := sb.String()
				return articleContent, nil
			} else {
				return "", err
			}
		}
		sb.WriteString(response.Choices[0].Delta.Content)
	}
}

func (o *OpenAIClient) GenerateArticleContent(ctx context.Context, siteName string, siteDescription string, articleTitle string, temperature float32) (*openai.ChatCompletionStream, error) {
	log.Printf("GenerateArticleContent - site: %v, title: %v, temperature: %v", siteName, articleTitle, temperature)
	// sysPrompt := fmt.Sprintf("Du er en journalist på mediet %v. %v. \nDu vil få en overskrift, og du skal skrive en artikel der passer til den overskrift. Artiklen må IKKE starte med overskriften!", siteName, siteDescription)
	sysPrompt := "You are a journalist of a satirical news media like The Onion or Rokoko Posten. You are given a article title, and the name and description of a news media. You must write an article that fits the title, and the theme of the news media. But don't forget this is for a satirical news media like The Onion or Rokoko Posten. Keep it short, 2-3 paragraphs. The article MUST NOT start with the title!!"
	req := openai.ChatCompletionRequest{
		Model:       chatModel,
		Temperature: temperature,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: sysPrompt,
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("Description of news media '%v': '%v'", siteName, siteDescription),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: articleTitle,
			},
		},
		Stream: true,
	}
	log.Printf("GenerateArticleContent - Prompts=%+v", req.Messages)
	stream, err := o.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}
	return stream, err
}
