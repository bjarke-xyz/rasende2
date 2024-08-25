package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var aiCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "rasende2_ai_counter",
	Help: "Counter of ai activities",
}, []string{"type"})

func AiCounterImageInc() {
	aiCounter.WithLabelValues("image").Inc()
}
func AiCounterTranslateInc() {
	aiCounter.WithLabelValues("translate").Inc()
}
func AiCounterImagePromptInc() {
	aiCounter.WithLabelValues("image_prompt").Inc()
}
func AiCounterTitlesInc() {
	aiCounter.WithLabelValues("title").Inc()
}
func AiCounterSelectTitleInc() {
	aiCounter.WithLabelValues("select_title").Inc()
}
func AiCounterArticleContentInc() {
	aiCounter.WithLabelValues("article_content").Inc()
}
