package pkg

import gonanoid "github.com/matoous/go-nanoid/v2"

const nanoidAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_"

func NewNanoid() (string, error) {
	return gonanoid.Generate(nanoidAlphabet, 21)
}
