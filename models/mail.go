// Copyright 2016 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"bytes"
	"fmt"
	"html/template"
	"path"

	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/mailer"
	"code.gitea.io/gitea/modules/markup"
	"code.gitea.io/gitea/modules/markup/markdown"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"
	"gopkg.in/gomail.v2"
)

const (
	mailAuthActivate       base.TplName = "auth/activate"
	mailAuthActivateEmail  base.TplName = "auth/activate_email"
	mailAuthResetPassword  base.TplName = "auth/reset_passwd"
	mailAuthRegisterNotify base.TplName = "auth/register_notify"

	mailIssueComment base.TplName = "issue/comment"
	mailIssueMention base.TplName = "issue/mention"

	mailNotifyCollaborator base.TplName = "notify/collaborator"
)

var templates *template.Template

// InitMailRender initializes the mail renderer
func InitMailRender(tmpls *template.Template) {
	templates = tmpls
}

// SendTestMail sends a test mail
func SendTestMail(email string) error {
	return gomail.Send(mailer.Sender, mailer.NewMessage([]string{email}, "Gitea Test Email!", "Gitea Test Email!").Message)
}

// SendUserMail sends a mail to the user
func SendUserMail(language string, u *User, tpl base.TplName, code, subject, info string) {
	data := map[string]interface{}{
		"DisplayName":       u.DisplayName(),
		"ActiveCodeLives":   timeutil.MinutesToFriendly(setting.Service.ActiveCodeLives, language),
		"ResetPwdCodeLives": timeutil.MinutesToFriendly(setting.Service.ResetPwdCodeLives, language),
		"Code":              code,
	}

	var content bytes.Buffer

	if err := templates.ExecuteTemplate(&content, string(tpl), data); err != nil {
		log.Error("Template: %v", err)
		return
	}

	msg := mailer.NewMessage([]string{u.Email}, subject, content.String())
	msg.Info = fmt.Sprintf("UID: %d, %s", u.ID, info)

	mailer.SendAsync(msg)
}

// Locale represents an interface to translation
type Locale interface {
	Language() string
	Tr(string, ...interface{}) string
}

// SendActivateAccountMail sends an activation mail to the user (new user registration)
func SendActivateAccountMail(locale Locale, u *User) {
	SendUserMail(locale.Language(), u, mailAuthActivate, u.GenerateActivateCode(), locale.Tr("mail.activate_account"), "activate account")
}

// SendResetPasswordMail sends a password reset mail to the user
func SendResetPasswordMail(locale Locale, u *User) {
	SendUserMail(locale.Language(), u, mailAuthResetPassword, u.GenerateActivateCode(), locale.Tr("mail.reset_password"), "recover account")
}

// SendActivateEmailMail sends confirmation email to confirm new email address
func SendActivateEmailMail(locale Locale, u *User, email *EmailAddress) {
	data := map[string]interface{}{
		"DisplayName":     u.DisplayName(),
		"ActiveCodeLives": timeutil.MinutesToFriendly(setting.Service.ActiveCodeLives, locale.Language()),
		"Code":            u.GenerateEmailActivateCode(email.Email),
		"Email":           email.Email,
	}

	var content bytes.Buffer

	if err := templates.ExecuteTemplate(&content, string(mailAuthActivateEmail), data); err != nil {
		log.Error("Template: %v", err)
		return
	}

	msg := mailer.NewMessage([]string{email.Email}, locale.Tr("mail.activate_email"), content.String())
	msg.Info = fmt.Sprintf("UID: %d, activate email", u.ID)

	mailer.SendAsync(msg)
}

// SendRegisterNotifyMail triggers a notify e-mail by admin created a account.
func SendRegisterNotifyMail(locale Locale, u *User) {
	data := map[string]interface{}{
		"DisplayName": u.DisplayName(),
		"Username":    u.Name,
	}

	var content bytes.Buffer

	if err := templates.ExecuteTemplate(&content, string(mailAuthRegisterNotify), data); err != nil {
		log.Error("Template: %v", err)
		return
	}

	msg := mailer.NewMessage([]string{u.Email}, locale.Tr("mail.register_notify"), content.String())
	msg.Info = fmt.Sprintf("UID: %d, registration notify", u.ID)

	mailer.SendAsync(msg)
}

// SendCollaboratorMail sends mail notification to new collaborator.
func SendCollaboratorMail(u, doer *User, repo *Repository) {
	repoName := path.Join(repo.Owner.Name, repo.Name)
	subject := fmt.Sprintf("%s added you to %s", doer.DisplayName(), repoName)

	data := map[string]interface{}{
		"Subject":  subject,
		"RepoName": repoName,
		"Link":     repo.HTMLURL(),
	}

	var content bytes.Buffer

	if err := templates.ExecuteTemplate(&content, string(mailNotifyCollaborator), data); err != nil {
		log.Error("Template: %v", err)
		return
	}

	msg := mailer.NewMessage([]string{u.Email}, subject, content.String())
	msg.Info = fmt.Sprintf("UID: %d, add collaborator", u.ID)

	mailer.SendAsync(msg)
}

func composeTplData(subject, body, link string) map[string]interface{} {
	data := make(map[string]interface{}, 10)
	data["Subject"] = subject
	data["Body"] = body
	data["Link"] = link
	return data
}

func composeIssueCommentMessage(issue *Issue, doer *User, content string, comment *Comment, tplName base.TplName, tos []string, info string) *mailer.Message {
	var subject string
	if comment != nil {
		subject = "Re: " + issue.mailSubject()
	} else {
		subject = issue.mailSubject()
	}

	err := issue.LoadRepo()
	if err != nil {
		log.Error("LoadRepo: %v", err)
	}
	body := string(markup.RenderByType(markdown.MarkupName, []byte(content), issue.Repo.HTMLURL(), issue.Repo.ComposeMetas()))

	var data = make(map[string]interface{}, 10)
	if comment != nil {
		data = composeTplData(subject, body, issue.HTMLURL()+"#"+comment.HashTag())
	} else {
		data = composeTplData(subject, body, issue.HTMLURL())
	}
	data["Doer"] = doer

	var mailBody bytes.Buffer

	if err := templates.ExecuteTemplate(&mailBody, string(tplName), data); err != nil {
		log.Error("Template: %v", err)
	}

	msg := mailer.NewMessageFrom(tos, doer.DisplayName(), setting.MailService.FromEmail, subject, mailBody.String())
	msg.Info = fmt.Sprintf("Subject: %s, %s", subject, info)

	// Set Message-ID on first message so replies know what to reference
	if comment == nil {
		msg.SetHeader("Message-ID", "<"+issue.ReplyReference()+">")
	} else {
		msg.SetHeader("In-Reply-To", "<"+issue.ReplyReference()+">")
		msg.SetHeader("References", "<"+issue.ReplyReference()+">")
	}

	return msg
}

// SendIssueCommentMail composes and sends issue comment emails to target receivers.
func SendIssueCommentMail(issue *Issue, doer *User, content string, comment *Comment, tos []string) {
	if len(tos) == 0 {
		return
	}

	mailer.SendAsync(composeIssueCommentMessage(issue, doer, content, comment, mailIssueComment, tos, "issue comment"))
}

// SendIssueMentionMail composes and sends issue mention emails to target receivers.
func SendIssueMentionMail(issue *Issue, doer *User, content string, comment *Comment, tos []string) {
	if len(tos) == 0 {
		return
	}
	mailer.SendAsync(composeIssueCommentMessage(issue, doer, content, comment, mailIssueMention, tos, "issue mention"))
}
