package pkg

import (
	"fmt"
	"log"
	"math"
	"net/smtp"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/config"
)

type MailService struct {
	cfg *config.Config
}

func NewMail(cfg *config.Config) *MailService {
	return &MailService{cfg: cfg}
}

type SendAuthLinkRequest struct {
	Receiver            string
	CodePath            string
	OTP                 string
	ExpirationTimestamp time.Time
}

func (m *MailService) SendAuthLink(req SendAuthLinkRequest) error {
	name := GetEmailPrefix(req.Receiver)
	expiresDiff := req.ExpirationTimestamp.UTC().Sub(time.Now().UTC())
	expiresInMinutes := math.Round(expiresDiff.Minutes())
	codeUrl := fmt.Sprintf("%v%v", m.cfg.BaseUrl, req.CodePath)
	formattedOtp := fmt.Sprintf("%s-%s", req.OTP[:3], req.OTP[3:])
	sendMailReq := SendMailRequest{
		Receiver: req.Receiver,
		Subject:  "Your link to sign in",
		Message: fmt.Sprintf(`
Hey %v,

Click here to sign in:

%v

This link expires in %v minutes.

Or enter this One-Time-Password (OTP):

%v

If you didn't ask for this, just ignore it.

-  Rasende`, name, codeUrl, expiresInMinutes, formattedOtp),
	}
	return m.Send(sendMailReq)
}

type SendMailRequest struct {
	Receiver string
	Subject  string
	Message  string
}

func (m *MailService) Send(req SendMailRequest) error {
	message := fmt.Sprintf("Subject: %v\n\n%v", req.Subject, req.Message)
	if m.cfg.SmtpTest {
		log.Printf("MAIL: SMTP_TEST = TRUE. Receiver=%v // Message:\n%v", req.Receiver, message)
		return nil
	}
	messageBytes := []byte(message)
	auth := smtp.PlainAuth("", m.cfg.SmtpUsername, m.cfg.SmtpPassword, m.cfg.SmtpHost)
	err := smtp.SendMail(m.cfg.SmtpHost+":"+m.cfg.SmtpPort, auth, m.cfg.SmtpSender, []string{req.Receiver}, messageBytes)
	if err != nil {
		log.Printf("error sending mail to %v: %v", req.Receiver, err)
		return err
	}
	return nil
}

// GetEmailPrefix returns the part of the email before the "@" symbol.
func GetEmailPrefix(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
