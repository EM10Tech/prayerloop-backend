package services

import (
	"fmt"
	"log"
	"os"

	"github.com/resend/resend-go/v2"
)

type EmailService struct {
	client *resend.Client
}

var emailService *EmailService

// InitEmailService initializes the email service with Resend API
func InitEmailService() {
	apiKey := os.Getenv("RESEND_API_KEY")

	if apiKey == "" {
		log.Println("WARNING: RESEND_API_KEY not set. Email service will not be available.")
		return
	}

	emailService = &EmailService{
		client: resend.NewClient(apiKey),
	}

	log.Println("Email service initialized successfully with Resend")
}

// GetEmailService returns the singleton email service instance
func GetEmailService() *EmailService {
	return emailService
}

// SendPasswordResetEmail sends a password reset email with a 6-digit code
func (s *EmailService) SendPasswordResetEmail(toEmail string, code string, firstName string) error {
	if s.client == nil {
		return fmt.Errorf("email service not initialized")
	}

	// Build the email HTML with the 6-digit code
	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
        }
        .header {
            text-align: center;
            padding: 20px 0;
            border-bottom: 2px solid #90c590;
        }
        .header h1 {
            color: #90c590;
            margin: 0;
        }
        .content {
            padding: 30px 0;
        }
        .code-container {
            background-color: #f5f5f5;
            border: 2px solid #90c590;
            border-radius: 8px;
            padding: 20px;
            text-align: center;
            margin: 20px 0;
        }
        .code {
            font-size: 32px;
            font-weight: bold;
            letter-spacing: 8px;
            color: #90c590;
            font-family: monospace;
        }
        .warning {
            background-color: #fff3cd;
            border: 1px solid #ffc107;
            border-radius: 4px;
            padding: 15px;
            margin: 20px 0;
        }
        .footer {
            text-align: center;
            padding: 20px 0;
            border-top: 1px solid #ddd;
            font-size: 12px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>prayerloop</h1>
    </div>

    <div class="content">
        <h2>Password Reset Request</h2>

        <p>Hi %s,</p>

        <p>We received a request to reset your prayerloop password. Use the verification code below to complete the password reset process:</p>

        <div class="code-container">
            <div class="code">%s</div>
        </div>

        <p><strong>This code will expire in 15 minutes.</strong></p>

        <div class="warning">
            <p><strong>⚠️ Security Notice:</strong></p>
            <p>If you didn't request a password reset, please ignore this email. Your password will remain unchanged.</p>
        </div>

        <p>Need help? Reply to this email or contact our support team.</p>

        <p>Blessings,<br>The prayerloop Team</p>
    </div>

    <div class="footer">
        <p>&copy; 2025 prayerloop. All rights reserved.</p>
        <p>This is an automated message, please do not reply directly to this email.</p>
    </div>
</body>
</html>
`, firstName, code)

	// Plain text fallback
	textBody := fmt.Sprintf(`
Password Reset Request

Hi %s,

We received a request to reset your prayerloop password. Use the verification code below to complete the password reset process:

Your verification code: %s

This code will expire in 15 minutes.

Security Notice:
If you didn't request a password reset, please ignore this email. Your password will remain unchanged.

Need help? Reply to this email or contact our support team.

Blessings,
The prayerloop Team
`, firstName, code)

	params := &resend.SendEmailRequest{
		From:    os.Getenv("RESEND_FROM_EMAIL"),
		To:      []string{toEmail},
		Subject: "Reset Your prayerloop Password",
		Html:    htmlBody,
		Text:    textBody,
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		log.Printf("Failed to send password reset email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Printf("Successfully sent password reset email to %s. Email ID: %s", toEmail, sent.Id)
	return nil
}

// SendWelcomeEmail sends a welcome email to new users (optional - for future use)
func (s *EmailService) SendWelcomeEmail(toEmail string, firstName string) error {
	if s.client == nil {
		return fmt.Errorf("email service not initialized")
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
        }
        .header {
            text-align: center;
            padding: 20px 0;
            border-bottom: 2px solid #90c590;
        }
        .header h1 {
            color: #90c590;
            margin: 0;
        }
        .content {
            padding: 30px 0;
        }
        .footer {
            text-align: center;
            padding: 20px 0;
            border-top: 1px solid #ddd;
            font-size: 12px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>Welcome to prayerloop</h1>
    </div>

    <div class="content">
        <h2>Welcome, %s!</h2>

        <p>Thank you so much for installing prayerloop! I hope that this app blesses you and your prayer life.</p>    

        <p>With prayerloop, you can:</p>
        <ul>
            <li>Organize your prayer requests in one place (no more lost prayers in text threads!)</li>
            <li>Create private groups with family, friends, and small groups</li>
            <li>Stay updated with push notifications when new prayers are shared</li>
            <li>Build a consistent prayer habit with reminders</li>
        </ul>

        <p><strong>Your feedback matters!</strong> If you have suggestions, questions, or run into any issues, please reach out to us at <a href="mailto:support@prayerloop.io" style="color: #90c590;">support@prayerloop.io</a>. We read every message and your input helps shape the future of this app.</p>

        <p>Blessings,<br>The prayerloop Team</p>
    </div>

    <div class="footer">
        <p>&copy; 2026 prayerloop. All rights reserved.</p>
    </div>
</body>
</html>
`, firstName)

	params := &resend.SendEmailRequest{
		From:    os.Getenv("RESEND_FROM_EMAIL"),
		To:      []string{toEmail},
		Subject: "Welcome to prayerloop!",
		Html:    htmlBody,
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		log.Printf("Failed to send welcome email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Printf("Successfully sent welcome email to %s. Email ID: %s", toEmail, sent.Id)
	return nil
}

// SendGroupLeftEmail sends an email when a user voluntarily leaves a group
func (s *EmailService) SendGroupLeftEmail(toEmail string, firstName string, groupName string) error {
	if s.client == nil {
		return fmt.Errorf("email service not initialized")
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
        }
        .header {
            text-align: center;
            padding: 20px 0;
            border-bottom: 2px solid #90c590;
        }
        .header h1 {
            color: #90c590;
            margin: 0;
        }
        .content {
            padding: 30px 0;
        }
        .footer {
            text-align: center;
            padding: 20px 0;
            border-top: 1px solid #ddd;
            font-size: 12px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>prayerloop</h1>
    </div>

    <div class="content">
        <h2>You Left a Group</h2>

        <p>Hi %s,</p>

        <p>This is a confirmation that you have left the group <strong>"%s"</strong>.</p>

        <p>You will no longer receive prayers or updates from this group. If you'd like to rejoin, please contact the group creator for a new invitation.</p>

        <p>Blessings,<br>The prayerloop Team</p>
    </div>

    <div class="footer">
        <p>&copy; 2026 prayerloop. All rights reserved.</p>
    </div>
</body>
</html>
`, firstName, groupName)

	textBody := fmt.Sprintf(`
You Left a Group

Hi %s,

This is a confirmation that you have left the group "%s".

You will no longer receive prayers or updates from this group. If you'd like to rejoin, please contact the group creator for a new invitation.

Blessings,
The prayerloop Team
`, firstName, groupName)

	params := &resend.SendEmailRequest{
		From:    os.Getenv("RESEND_FROM_EMAIL"),
		To:      []string{toEmail},
		Subject: fmt.Sprintf("You left \"%s\" on prayerloop", groupName),
		Html:    htmlBody,
		Text:    textBody,
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		log.Printf("Failed to send group left email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Printf("Successfully sent group left email to %s. Email ID: %s", toEmail, sent.Id)
	return nil
}

// SendGroupDeletedEmail sends an email to all members when a group is deleted
func (s *EmailService) SendGroupDeletedEmail(toEmail string, firstName string, groupName string) error {
	if s.client == nil {
		return fmt.Errorf("email service not initialized")
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
        }
        .header {
            text-align: center;
            padding: 20px 0;
            border-bottom: 2px solid #90c590;
        }
        .header h1 {
            color: #90c590;
            margin: 0;
        }
        .content {
            padding: 30px 0;
        }
        .footer {
            text-align: center;
            padding: 20px 0;
            border-top: 1px solid #ddd;
            font-size: 12px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>prayerloop</h1>
    </div>

    <div class="content">
        <h2>Group Deleted</h2>

        <p>Hi %s,</p>

        <p>The group <strong>"%s"</strong> has been deleted by its creator.</p>

        <p>You no longer have access to this group or its prayers. If you believe this was done in error, please contact the group creator.</p>

        <p>Blessings,<br>The prayerloop Team</p>
    </div>

    <div class="footer">
        <p>&copy; 2025 prayerloop. All rights reserved.</p>
    </div>
</body>
</html>
`, firstName, groupName)

	textBody := fmt.Sprintf(`
Group Deleted

Hi %s,

The group "%s" has been deleted by its creator.

You no longer have access to this group or its prayers. If you believe this was done in error, please contact the group creator.

Blessings,
The prayerloop Team
`, firstName, groupName)

	params := &resend.SendEmailRequest{
		From:    os.Getenv("RESEND_FROM_EMAIL"),
		To:      []string{toEmail},
		Subject: fmt.Sprintf("\"%s\" has been deleted", groupName),
		Html:    htmlBody,
		Text:    textBody,
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		log.Printf("Failed to send group deleted email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Printf("Successfully sent group deleted email to %s. Email ID: %s", toEmail, sent.Id)
	return nil
}

// SendRemovedFromGroupEmail sends an email when a user is removed from a group by the creator
func (s *EmailService) SendRemovedFromGroupEmail(toEmail string, firstName string, groupName string) error {
	if s.client == nil {
		return fmt.Errorf("email service not initialized")
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
        }
        .header {
            text-align: center;
            padding: 20px 0;
            border-bottom: 2px solid #90c590;
        }
        .header h1 {
            color: #90c590;
            margin: 0;
        }
        .content {
            padding: 30px 0;
        }
        .footer {
            text-align: center;
            padding: 20px 0;
            border-top: 1px solid #ddd;
            font-size: 12px;
            color: #666;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>prayerloop</h1>
    </div>

    <div class="content">
        <h2>Removed from Group</h2>

        <p>Hi %s,</p>

        <p>You have been removed from the group <strong>"%s"</strong> by the group creator.</p>

        <p>You no longer have access to this group or its prayers. If you have questions about this, please contact the group creator.</p>

        <p>Blessings,<br>The prayerloop Team</p>
    </div>

    <div class="footer">
        <p>&copy; 2025 prayerloop. All rights reserved.</p>
    </div>
</body>
</html>
`, firstName, groupName)

	textBody := fmt.Sprintf(`
Removed from Group

Hi %s,

You have been removed from the group "%s" by the group creator.

You no longer have access to this group or its prayers. If you have questions about this, please contact the group creator.

Blessings,
The prayerloop Team
`, firstName, groupName)

	params := &resend.SendEmailRequest{
		From:    os.Getenv("RESEND_FROM_EMAIL"),
		To:      []string{toEmail},
		Subject: fmt.Sprintf("You were removed from \"%s\"", groupName),
		Html:    htmlBody,
		Text:    textBody,
	}

	sent, err := s.client.Emails.Send(params)
	if err != nil {
		log.Printf("Failed to send removed from group email to %s: %v", toEmail, err)
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Printf("Successfully sent removed from group email to %s. Email ID: %s", toEmail, sent.Id)
	return nil
}
