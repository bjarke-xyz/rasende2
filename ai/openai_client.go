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

	promptReq := openai.ChatCompletionRequest{
		Model:       chatModel,
		Temperature: 1,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: imgLlmPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: articleTitle,
			},
		},
		Stream: false,
	}
	prompt := fmt.Sprintf("Create a header image for an article titled '%v', to be used on %v. %v. **Do not include any text, such as the newspaper name, article title, or any other wording, in the image.**", articleTitle, siteName, siteDescription)
	promptResp, err := o.client.CreateChatCompletion(ctx, promptReq)
	if err != nil {
		log.Printf("prompt request failed: %v", err)
	} else {
		if len(promptResp.Choices) > 0 {
			newPrompt := promptResp.Choices[0].Message.Content
			if newPrompt != "" {
				prompt = newPrompt
			}
		}
	}

	imgReq := openai.ImageRequest{
		Model: "dall-e-3",
		// Prompt:         fmt.Sprintf("Et billede der passer til en artikel på nyhedsmediet %s. %s. Artiklens overskrift er '%s'. Billedet skal passe til artiklen. Billedet bør IKKE ligne en artikel eller en avis, eller indeholde aviser. Billedet skal være passende til artiklen. Artiklen vises på en hjemmeside.", siteName, siteDescription, articleTitle),
		// Prompt:         fmt.Sprintf("Create a header image for an article titled '%v'. The article will be published in an online news media called '%v'", articleTitle, siteName),
		Prompt:         prompt,
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
	sysPrompt := fmt.Sprintf("You are a journalist on a satirical news media like The Onion or Rokoko Posten. You must come up with new article titles, in the style of the news media '%v', but they must be fun and satirical so they can get published in The Onion or Rokoko Posten. You will be provided a description of the news media '%v', and a list of previous article titles from that news media. Start each title on a new line. Start each line with a space (' '). Return only titles, nothing else. Make at most %v titles. The titles MUST be danish!. The titles MUST start with a capital letter.", siteName, siteName, newTitlesCount)
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
	// sysPrompt := "You are a journalist of a satirical news media like The Onion or Rokoko Posten. You are given a article title, and the name and description of a news media. You must write an article that fits the title, and the theme of the news media. But don't forget this is for a satirical news media like The Onion or Rokoko Posten. Keep it short, 2-3 paragraphs. The article MUST NOT start with the title!!"
	sysPrompt := "Du er en journalist på et satirisk nyhedsmedie, som f.eks. The Onion eller Rokoko Posten. Du vil få en overskrift, og navn og beskrivelse af et nyhedsmedia. Du skal skrive en artikel der passer til titlen, og nyhedsmediet. HUSK at artiklen skal publiceres i et satirisk nyhedsmedie som Rokoko Posten eller The Onion. Hold det kort, 2-3 afsnit. Artiklen MÅ IKKE starte med overskriften!!"
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
				Content: fmt.Sprintf("Beskrivelse af nyhedsmediet '%v': '%v'", siteName, siteDescription),
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

var imgLlmPrompt string = `
I am going to provide you guidelines for prompting flux.1 image AI. I am also going to provide you a news media article title. I want you to give me a prompt, that will generate a good article header images. Only return the prompt text, nothing else.


# Prompt guidelines:
Prompt Crafting Techniques

Note: All examples were created with the FLUX.1 Schnell model from GizAI’s AI Image Generator.
1. Be Specific and Descriptive

FLUX.1 thrives on detailed information. Instead of vague descriptions, provide specific details about your subject and scene.

Poor: “A portrait of a woman”
Better: “A close-up portrait of a middle-aged woman with curly red hair, green eyes, and freckles, wearing a blue silk blouse”

Example Prompt: A hyperrealistic portrait of a weathered sailor in his 60s, with deep-set blue eyes, a salt-and-pepper beard, and sun-weathered skin. He’s wearing a faded blue captain’s hat and a thick wool sweater. The background shows a misty harbor at dawn, with fishing boats barely visible in the distance.

2. Use Artistic References

Referencing specific artists, art movements, or styles can help guide FLUX.1’s output.

Example Prompt: Create an image in the style of Vincent van Gogh’s “Starry Night,” but replace the village with a futuristic cityscape. Maintain the swirling, expressive brushstrokes and vibrant color palette of the original, emphasizing deep blues and bright yellows. The city should have tall, glowing skyscrapers that blend seamlessly with the swirling sky.

3. Specify Technical Details

Including camera settings, angles, and other technical aspects can significantly influence the final image.

Example Prompt: Capture a street food vendor in Tokyo at night, shot with a wide-angle lens (24mm) at f/1.8. Use a shallow depth of field to focus on the vendor’s hands preparing takoyaki, with the glowing street signs and bustling crowd blurred in the background. High ISO setting to capture the ambient light, giving the image a slight grain for a cinematic feel.

4. Blend Concepts

FLUX.1 excels at combining different ideas or themes to create unique images.

Example Prompt: Illustrate “The Last Supper” by Leonardo da Vinci, but reimagine it with robots in a futuristic setting. Maintain the composition and dramatic lighting of the original painting, but replace the apostles with various types of androids and cyborgs. The table should be a long, sleek metal surface with holographic displays. In place of bread and wine, have the robots interfacing with glowing data streams.

5. Use Contrast and Juxtaposition

Creating contrast within your prompt can lead to visually striking and thought-provoking images.

Example Prompt: Create an image that juxtaposes the delicate beauty of nature with the harsh reality of urban decay. Show a vibrant cherry blossom tree in full bloom growing out of a cracked concrete sidewalk in a dilapidated city alley. The tree should be the focal point, with its pink petals contrasting against the gray, graffiti-covered walls of surrounding buildings. Include a small bird perched on one of the branches to emphasize the theme of resilience.

6. Incorporate Mood and Atmosphere

Describing the emotional tone or atmosphere can help FLUX.1 generate images with the desired feel.

Example Prompt: Depict a cozy, warmly lit bookstore cafe on a rainy evening. The atmosphere should be inviting and nostalgic, with soft yellow lighting from vintage lamps illuminating rows of well-worn books. Show patrons reading in comfortable armchairs, steam rising from their coffee cups. The large front window should reveal a glistening wet street outside, with blurred lights from passing cars. Emphasize the contrast between the warm interior and the cool, rainy exterior.

7. Leverage FLUX.1’s Text Rendering Capabilities

FLUX.1’s superior text rendering allows for creative use of text within images.

Example Prompt: Create a surreal advertisement poster for a fictional time travel agency. The background should depict a swirling vortex of clock faces and historical landmarks from different eras. In the foreground, place large, bold text that reads “CHRONO TOURS: YOUR PAST IS OUR FUTURE” in a retro-futuristic font. The text should appear to be partially disintegrating into particles that are being sucked into the time vortex. Include smaller text at the bottom with fictional pricing and the slogan “History is just a ticket away!”

8. Experiment with Unusual Perspectives

Challenging FLUX.1 with unique viewpoints can result in visually interesting images.

Example Prompt: Illustrate a “bug’s-eye view” of a picnic in a lush garden. The perspective should be from ground level, looking up at towering blades of grass and wildflowers that frame the scene. In the distance, show the underside of a red and white checkered picnic blanket with the silhouettes of picnic foods and human figures visible through the semi-transparent fabric. Include a few ants in the foreground carrying crumbs, and a ladybug climbing a blade of grass. The lighting should be warm and dappled, as if filtering through leaves.

Advanced Techniques
1. Layered Prompts

For complex scenes, consider breaking down your prompt into layers, focusing on different elements of the image.

Example Prompt: Create a bustling marketplace in a fantastical floating city.

Layer 1 (Background): Depict a city of interconnected floating islands suspended in a pastel sky. The islands should have a mix of whimsical architecture styles, from towering spires to quaint cottages. Show distant airships and flying creatures in the background.

Layer 2 (Middle ground): Focus on the main marketplace area. Illustrate a wide plaza with colorful stalls and shops selling exotic goods. Include floating platforms that serve as walkways between different sections of the market.

Layer 3 (Foreground): Populate the scene with a diverse array of fantasy creatures and humanoids. Show vendors calling out to customers, children chasing magical floating bubbles, and a street performer juggling balls of light. In the immediate foreground, depict a detailed stall selling glowing potions and mystical artifacts.

Atmosphere: The overall mood should be vibrant and magical, with soft, ethereal lighting that emphasizes the fantastical nature of the scene.

2. Style Fusion

Combine multiple artistic styles to create unique visual experiences.

Example Prompt: Create an image that fuses the precision of M.C. Escher’s impossible geometries with the bold colors and shapes of Wassily Kandinsky’s abstract compositions. The subject should be a surreal cityscape where buildings seamlessly transform into musical instruments. Use Escher’s techniques to create paradoxical perspectives and interconnected structures, but render them in Kandinsky’s vibrant, non-representational style. Incorporate musical notations and abstract shapes that flow through the scene, connecting the architectural elements. The color palette should be rich and varied, with particular emphasis on deep blues, vibrant reds, and golden yellows.

3. Temporal Narratives

Challenge FLUX.1 to convey a sense of time passing or a story unfolding within a single image.

Example Prompt: Illustrate the life cycle of a monarch butterfly in a single, continuous image. Divide the canvas into four seamlessly blending sections, each representing a stage of the butterfly’s life.

Start on the left with a milkweed plant where tiny eggs are visible on the underside of a leaf. As we move right, show the caterpillar stage with the larva feeding on milkweed leaves. In the third section, depict the chrysalis stage, with the green and gold-flecked pupa hanging from a branch.

Finally, on the right side, show the fully formed adult butterfly emerging, with its wings gradually opening to reveal the iconic orange and black pattern. Use a soft, natural color palette dominated by greens and oranges. The background should subtly shift from spring to summer as we move from left to right, with changing foliage and lighting to indicate the passage of time.

4. Emotional Gradients

Direct FLUX.1 to create images that convey a progression of emotions or moods.

Example Prompt: Create a panoramic image that depicts the progression of a person’s emotional journey from despair to hope. The scene should be a long, winding road that starts in a dark, stormy landscape and gradually transitions to a bright, sunlit meadow.

On the left, begin with a lone figure hunched against the wind, surrounded by bare, twisted trees and ominous storm clouds. As we move right, show the gradual clearing of the sky, with the road passing through a misty forest where hints of light begin to break through.

Continue the transition with the forest opening up to reveal distant mountains and a rainbow. The figure should become more upright and purposeful in their stride. Finally, on the far right, show the person standing tall in a sunlit meadow full of wildflowers, arms outstretched in a gesture of triumph or liberation.

Use color and lighting to enhance the emotional journey: start with a dark, desaturated palette on the left, gradually introducing more color and brightness as we move right, ending in a vibrant, warm color scheme. The overall composition should create a powerful visual metaphor for overcoming adversity and finding hope.

Tips for Optimal Results

    Experiment with Different Versions: FLUX.1 comes in different variants (Pro, Dev, and Schnell). Experiment with each to find the best fit for your needs.

    Iterate and Refine: Don’t be afraid to generate multiple images and refine your prompt based on the results.

    Balance Detail and Freedom: While specific details can guide FLUX.1, leaving some aspects open to interpretation can lead to surprising and creative results.

    Use Natural Language: FLUX.1 understands natural language, so write your prompts in a clear, descriptive manner rather than using keyword-heavy language.

    Explore Diverse Themes: FLUX.1 has a broad knowledge base, so don’t hesitate to explore various subjects, from historical scenes to futuristic concepts.

    Leverage Technical Terms: When appropriate, use photography, art, or design terminology to guide the image creation process.

    Consider Emotional Impact: Think about the feeling or message you want to convey and incorporate emotional cues into your prompt.

Common Pitfalls to Avoid

    Overloading the Prompt: While FLUX.1 can handle complex prompts, overloading with too many conflicting ideas can lead to confused outputs.

    Neglecting Composition: Don’t forget to guide the overall composition of the image, not just individual elements.

    Ignoring Lighting and Atmosphere: These elements greatly influence the mood and realism of the generated image.

    Being Too Vague: Extremely general prompts may lead to generic or unpredictable results.

    Forgetting About Style: Unless specified, FLUX.1 may default to a realistic style. Always indicate if you want a particular artistic approach.

Conclusion

Mastering FLUX.1 prompt engineering is a journey of creativity and experimentation. This guide provides a solid foundation, but the true potential of FLUX.1 lies in your imagination. As you practice and refine your prompting skills, you’ll discover new ways to bring your ideas to life with unprecedented detail and accuracy.

Remember, the key to success with FLUX.1 is balancing specificity with creative freedom. Provide enough detail to guide the model, but also leave room for FLUX.1 to surprise you with its interpretations. Happy creating!

`
