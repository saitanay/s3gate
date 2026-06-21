package web

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
)

func SendMagicLink(email, token string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")
	baseURL := os.Getenv("BASE_URL")

	link := fmt.Sprintf("%s/auth/verify?token=%s", baseURL, token)

	subject := "Sign in to BucketCheap"
	body := fmt.Sprintf(`Hi,

Click this link to sign in to BucketCheap:

%s

This link expires in 15 minutes. If you didn't request this, ignore this email.

— BucketCheap`, link)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, email, subject, body)

	auth := smtp.PlainAuth("", user, pass, host)
	addr := fmt.Sprintf("%s:%s", host, port)

	err := smtp.SendMail(addr, auth, from, []string{email}, []byte(msg))
	if err != nil {
		log.Printf("ERROR sending email to %s: %v", email, err)
		return err
	}

	log.Printf("Magic link sent to %s", email)
	return nil
}
