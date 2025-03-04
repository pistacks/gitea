// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"encoding/json"
	"fmt"
	"html"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	api "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/gitea/modules/timeutil"

	"github.com/Unknwon/com"
	"xorm.io/builder"
)

// ActionType represents the type of an action.
type ActionType int

// Possible action types.
const (
	ActionCreateRepo        ActionType = iota + 1 // 1
	ActionRenameRepo                              // 2
	ActionStarRepo                                // 3
	ActionWatchRepo                               // 4
	ActionCommitRepo                              // 5
	ActionCreateIssue                             // 6
	ActionCreatePullRequest                       // 7
	ActionTransferRepo                            // 8
	ActionPushTag                                 // 9
	ActionCommentIssue                            // 10
	ActionMergePullRequest                        // 11
	ActionCloseIssue                              // 12
	ActionReopenIssue                             // 13
	ActionClosePullRequest                        // 14
	ActionReopenPullRequest                       // 15
	ActionDeleteTag                               // 16
	ActionDeleteBranch                            // 17
	ActionMirrorSyncPush                          // 18
	ActionMirrorSyncCreate                        // 19
	ActionMirrorSyncDelete                        // 20
)

var (
	// Same as GitHub. See
	// https://help.github.com/articles/closing-issues-via-commit-messages
	issueCloseKeywords  = []string{"close", "closes", "closed", "fix", "fixes", "fixed", "resolve", "resolves", "resolved"}
	issueReopenKeywords = []string{"reopen", "reopens", "reopened"}

	issueCloseKeywordsPat, issueReopenKeywordsPat *regexp.Regexp
	issueReferenceKeywordsPat                     *regexp.Regexp
)

const issueRefRegexpStr = `(?:([0-9a-zA-Z-_\.]+)/([0-9a-zA-Z-_\.]+))?(#[0-9]+)+`
const issueRefRegexpStrNoKeyword = `(?:\s|^|\(|\[)(?:([0-9a-zA-Z-_\.]+)/([0-9a-zA-Z-_\.]+))?(#[0-9]+)(?:\s|$|\)|\]|\.(\s|$))`

func assembleKeywordsPattern(words []string) string {
	return fmt.Sprintf(`(?i)(?:%s)(?::?) %s`, strings.Join(words, "|"), issueRefRegexpStr)
}

func init() {
	issueCloseKeywordsPat = regexp.MustCompile(assembleKeywordsPattern(issueCloseKeywords))
	issueReopenKeywordsPat = regexp.MustCompile(assembleKeywordsPattern(issueReopenKeywords))
	issueReferenceKeywordsPat = regexp.MustCompile(issueRefRegexpStrNoKeyword)
}

// Action represents user operation type and other information to
// repository. It implemented interface base.Actioner so that can be
// used in template render.
type Action struct {
	ID          int64 `xorm:"pk autoincr"`
	UserID      int64 `xorm:"INDEX"` // Receiver user id.
	OpType      ActionType
	ActUserID   int64       `xorm:"INDEX"` // Action user id.
	ActUser     *User       `xorm:"-"`
	RepoID      int64       `xorm:"INDEX"`
	Repo        *Repository `xorm:"-"`
	CommentID   int64       `xorm:"INDEX"`
	Comment     *Comment    `xorm:"-"`
	IsDeleted   bool        `xorm:"INDEX NOT NULL DEFAULT false"`
	RefName     string
	IsPrivate   bool               `xorm:"INDEX NOT NULL DEFAULT false"`
	Content     string             `xorm:"TEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
}

// GetOpType gets the ActionType of this action.
func (a *Action) GetOpType() ActionType {
	return a.OpType
}

func (a *Action) loadActUser() {
	if a.ActUser != nil {
		return
	}
	var err error
	a.ActUser, err = GetUserByID(a.ActUserID)
	if err == nil {
		return
	} else if IsErrUserNotExist(err) {
		a.ActUser = NewGhostUser()
	} else {
		log.Error("GetUserByID(%d): %v", a.ActUserID, err)
	}
}

func (a *Action) loadRepo() {
	if a.Repo != nil {
		return
	}
	var err error
	a.Repo, err = GetRepositoryByID(a.RepoID)
	if err != nil {
		log.Error("GetRepositoryByID(%d): %v", a.RepoID, err)
	}
}

// GetActFullName gets the action's user full name.
func (a *Action) GetActFullName() string {
	a.loadActUser()
	return a.ActUser.FullName
}

// GetActUserName gets the action's user name.
func (a *Action) GetActUserName() string {
	a.loadActUser()
	return a.ActUser.Name
}

// ShortActUserName gets the action's user name trimmed to max 20
// chars.
func (a *Action) ShortActUserName() string {
	return base.EllipsisString(a.GetActUserName(), 20)
}

// GetDisplayName gets the action's display name based on DEFAULT_SHOW_FULL_NAME
func (a *Action) GetDisplayName() string {
	if setting.UI.DefaultShowFullName {
		return a.GetActFullName()
	}
	return a.ShortActUserName()
}

// GetDisplayNameTitle gets the action's display name used for the title (tooltip) based on DEFAULT_SHOW_FULL_NAME
func (a *Action) GetDisplayNameTitle() string {
	if setting.UI.DefaultShowFullName {
		return a.ShortActUserName()
	}
	return a.GetActFullName()
}

// GetActAvatar the action's user's avatar link
func (a *Action) GetActAvatar() string {
	a.loadActUser()
	return a.ActUser.RelAvatarLink()
}

// GetRepoUserName returns the name of the action repository owner.
func (a *Action) GetRepoUserName() string {
	a.loadRepo()
	return a.Repo.MustOwner().Name
}

// ShortRepoUserName returns the name of the action repository owner
// trimmed to max 20 chars.
func (a *Action) ShortRepoUserName() string {
	return base.EllipsisString(a.GetRepoUserName(), 20)
}

// GetRepoName returns the name of the action repository.
func (a *Action) GetRepoName() string {
	a.loadRepo()
	return a.Repo.Name
}

// ShortRepoName returns the name of the action repository
// trimmed to max 33 chars.
func (a *Action) ShortRepoName() string {
	return base.EllipsisString(a.GetRepoName(), 33)
}

// GetRepoPath returns the virtual path to the action repository.
func (a *Action) GetRepoPath() string {
	return path.Join(a.GetRepoUserName(), a.GetRepoName())
}

// ShortRepoPath returns the virtual path to the action repository
// trimmed to max 20 + 1 + 33 chars.
func (a *Action) ShortRepoPath() string {
	return path.Join(a.ShortRepoUserName(), a.ShortRepoName())
}

// GetRepoLink returns relative link to action repository.
func (a *Action) GetRepoLink() string {
	if len(setting.AppSubURL) > 0 {
		return path.Join(setting.AppSubURL, a.GetRepoPath())
	}
	return "/" + a.GetRepoPath()
}

// GetRepositoryFromMatch returns a *Repository from a username and repo strings
func GetRepositoryFromMatch(ownerName string, repoName string) (*Repository, error) {
	var err error
	refRepo, err := GetRepositoryByOwnerAndName(ownerName, repoName)
	if err != nil {
		if IsErrRepoNotExist(err) {
			log.Warn("Repository referenced in commit but does not exist: %v", err)
			return nil, err
		}
		log.Error("GetRepositoryByOwnerAndName: %v", err)
		return nil, err
	}
	return refRepo, nil
}

// GetCommentLink returns link to action comment.
func (a *Action) GetCommentLink() string {
	return a.getCommentLink(x)
}

func (a *Action) getCommentLink(e Engine) string {
	if a == nil {
		return "#"
	}
	if a.Comment == nil && a.CommentID != 0 {
		a.Comment, _ = GetCommentByID(a.CommentID)
	}
	if a.Comment != nil {
		return a.Comment.HTMLURL()
	}
	if len(a.GetIssueInfos()) == 0 {
		return "#"
	}
	//Return link to issue
	issueIDString := a.GetIssueInfos()[0]
	issueID, err := strconv.ParseInt(issueIDString, 10, 64)
	if err != nil {
		return "#"
	}

	issue, err := getIssueByID(e, issueID)
	if err != nil {
		return "#"
	}

	if err = issue.loadRepo(e); err != nil {
		return "#"
	}

	return issue.HTMLURL()
}

// GetBranch returns the action's repository branch.
func (a *Action) GetBranch() string {
	return a.RefName
}

// GetContent returns the action's content.
func (a *Action) GetContent() string {
	return a.Content
}

// GetCreate returns the action creation time.
func (a *Action) GetCreate() time.Time {
	return a.CreatedUnix.AsTime()
}

// GetIssueInfos returns a list of issues associated with
// the action.
func (a *Action) GetIssueInfos() []string {
	return strings.SplitN(a.Content, "|", 2)
}

// GetIssueTitle returns the title of first issue associated
// with the action.
func (a *Action) GetIssueTitle() string {
	index := com.StrTo(a.GetIssueInfos()[0]).MustInt64()
	issue, err := GetIssueByIndex(a.RepoID, index)
	if err != nil {
		log.Error("GetIssueByIndex: %v", err)
		return "500 when get issue"
	}
	return issue.Title
}

// GetIssueContent returns the content of first issue associated with
// this action.
func (a *Action) GetIssueContent() string {
	index := com.StrTo(a.GetIssueInfos()[0]).MustInt64()
	issue, err := GetIssueByIndex(a.RepoID, index)
	if err != nil {
		log.Error("GetIssueByIndex: %v", err)
		return "500 when get issue"
	}
	return issue.Content
}

func newRepoAction(e Engine, u *User, repo *Repository) (err error) {
	if err = notifyWatchers(e, &Action{
		ActUserID: u.ID,
		ActUser:   u,
		OpType:    ActionCreateRepo,
		RepoID:    repo.ID,
		Repo:      repo,
		IsPrivate: repo.IsPrivate,
	}); err != nil {
		return fmt.Errorf("notify watchers '%d/%d': %v", u.ID, repo.ID, err)
	}

	log.Trace("action.newRepoAction: %s/%s", u.Name, repo.Name)
	return err
}

// NewRepoAction adds new action for creating repository.
func NewRepoAction(u *User, repo *Repository) (err error) {
	return newRepoAction(x, u, repo)
}

func renameRepoAction(e Engine, actUser *User, oldRepoName string, repo *Repository) (err error) {
	if err = notifyWatchers(e, &Action{
		ActUserID: actUser.ID,
		ActUser:   actUser,
		OpType:    ActionRenameRepo,
		RepoID:    repo.ID,
		Repo:      repo,
		IsPrivate: repo.IsPrivate,
		Content:   oldRepoName,
	}); err != nil {
		return fmt.Errorf("notify watchers: %v", err)
	}

	log.Trace("action.renameRepoAction: %s/%s", actUser.Name, repo.Name)
	return nil
}

// RenameRepoAction adds new action for renaming a repository.
func RenameRepoAction(actUser *User, oldRepoName string, repo *Repository) error {
	return renameRepoAction(x, actUser, oldRepoName, repo)
}

func issueIndexTrimRight(c rune) bool {
	return !unicode.IsDigit(c)
}

// PushCommit represents a commit in a push operation.
type PushCommit struct {
	Sha1           string
	Message        string
	AuthorEmail    string
	AuthorName     string
	CommitterEmail string
	CommitterName  string
	Timestamp      time.Time
}

// PushCommits represents list of commits in a push operation.
type PushCommits struct {
	Len        int
	Commits    []*PushCommit
	CompareURL string

	avatars    map[string]string
	emailUsers map[string]*User
}

// NewPushCommits creates a new PushCommits object.
func NewPushCommits() *PushCommits {
	return &PushCommits{
		avatars:    make(map[string]string),
		emailUsers: make(map[string]*User),
	}
}

// ToAPIPayloadCommits converts a PushCommits object to
// api.PayloadCommit format.
func (pc *PushCommits) ToAPIPayloadCommits(repoPath, repoLink string) ([]*api.PayloadCommit, error) {
	commits := make([]*api.PayloadCommit, len(pc.Commits))

	if pc.emailUsers == nil {
		pc.emailUsers = make(map[string]*User)
	}
	var err error
	for i, commit := range pc.Commits {
		authorUsername := ""
		author, ok := pc.emailUsers[commit.AuthorEmail]
		if !ok {
			author, err = GetUserByEmail(commit.AuthorEmail)
			if err == nil {
				authorUsername = author.Name
				pc.emailUsers[commit.AuthorEmail] = author
			}
		} else {
			authorUsername = author.Name
		}

		committerUsername := ""
		committer, ok := pc.emailUsers[commit.CommitterEmail]
		if !ok {
			committer, err = GetUserByEmail(commit.CommitterEmail)
			if err == nil {
				// TODO: check errors other than email not found.
				committerUsername = committer.Name
				pc.emailUsers[commit.CommitterEmail] = committer
			}
		} else {
			committerUsername = committer.Name
		}

		fileStatus, err := git.GetCommitFileStatus(repoPath, commit.Sha1)
		if err != nil {
			return nil, fmt.Errorf("FileStatus [commit_sha1: %s]: %v", commit.Sha1, err)
		}

		commits[i] = &api.PayloadCommit{
			ID:      commit.Sha1,
			Message: commit.Message,
			URL:     fmt.Sprintf("%s/commit/%s", repoLink, commit.Sha1),
			Author: &api.PayloadUser{
				Name:     commit.AuthorName,
				Email:    commit.AuthorEmail,
				UserName: authorUsername,
			},
			Committer: &api.PayloadUser{
				Name:     commit.CommitterName,
				Email:    commit.CommitterEmail,
				UserName: committerUsername,
			},
			Added:     fileStatus.Added,
			Removed:   fileStatus.Removed,
			Modified:  fileStatus.Modified,
			Timestamp: commit.Timestamp,
		}
	}
	return commits, nil
}

// AvatarLink tries to match user in database with e-mail
// in order to show custom avatar, and falls back to general avatar link.
func (pc *PushCommits) AvatarLink(email string) string {
	if pc.avatars == nil {
		pc.avatars = make(map[string]string)
	}
	avatar, ok := pc.avatars[email]
	if ok {
		return avatar
	}

	u, ok := pc.emailUsers[email]
	if !ok {
		var err error
		u, err = GetUserByEmail(email)
		if err != nil {
			pc.avatars[email] = base.AvatarLink(email)
			if !IsErrUserNotExist(err) {
				log.Error("GetUserByEmail: %v", err)
				return ""
			}
		} else {
			pc.emailUsers[email] = u
		}
	}
	if u != nil {
		pc.avatars[email] = u.RelAvatarLink()
	}

	return pc.avatars[email]
}

// getIssueFromRef returns the issue referenced by a ref. Returns a nil *Issue
// if the provided ref is misformatted or references a non-existent issue.
func getIssueFromRef(repo *Repository, ref string) (*Issue, error) {
	ref = ref[strings.IndexByte(ref, ' ')+1:]
	ref = strings.TrimRightFunc(ref, issueIndexTrimRight)

	var refRepo *Repository
	poundIndex := strings.IndexByte(ref, '#')
	if poundIndex < 0 {
		return nil, nil
	} else if poundIndex == 0 {
		refRepo = repo
	} else {
		slashIndex := strings.IndexByte(ref, '/')
		if slashIndex < 0 || slashIndex >= poundIndex {
			return nil, nil
		}
		ownerName := ref[:slashIndex]
		repoName := ref[slashIndex+1 : poundIndex]
		var err error
		refRepo, err = GetRepositoryByOwnerAndName(ownerName, repoName)
		if err != nil {
			if IsErrRepoNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
	}
	issueIndex, err := strconv.ParseInt(ref[poundIndex+1:], 10, 64)
	if err != nil {
		return nil, nil
	}

	issue, err := GetIssueByIndex(refRepo.ID, issueIndex)
	if err != nil {
		if IsErrIssueNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return issue, nil
}

func changeIssueStatus(repo *Repository, doer *User, ref string, refMarked map[int64]bool, status bool) error {
	issue, err := getIssueFromRef(repo, ref)
	if err != nil {
		return err
	}

	if issue == nil || refMarked[issue.ID] {
		return nil
	}
	refMarked[issue.ID] = true

	if issue.RepoID != repo.ID || issue.IsClosed == status {
		return nil
	}

	stopTimerIfAvailable := func(doer *User, issue *Issue) error {

		if StopwatchExists(doer.ID, issue.ID) {
			if err := CreateOrStopIssueStopwatch(doer, issue); err != nil {
				return err
			}
		}

		return nil
	}

	issue.Repo = repo
	if err = issue.ChangeStatus(doer, status); err != nil {
		// Don't return an error when dependencies are open as this would let the push fail
		if IsErrDependenciesLeft(err) {
			return stopTimerIfAvailable(doer, issue)
		}
		return err
	}

	return stopTimerIfAvailable(doer, issue)
}

// UpdateIssuesCommit checks if issues are manipulated by commit message.
func UpdateIssuesCommit(doer *User, repo *Repository, commits []*PushCommit, branchName string) error {
	// Commits are appended in the reverse order.
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]

		refMarked := make(map[int64]bool)
		var refRepo *Repository
		var err error
		for _, m := range issueReferenceKeywordsPat.FindAllStringSubmatch(c.Message, -1) {
			if len(m[3]) == 0 {
				continue
			}
			ref := m[3]

			// issue is from another repo
			if len(m[1]) > 0 && len(m[2]) > 0 {
				refRepo, err = GetRepositoryFromMatch(m[1], m[2])
				if err != nil {
					continue
				}
			} else {
				refRepo = repo
			}
			issue, err := getIssueFromRef(refRepo, ref)
			if err != nil {
				return err
			}

			if issue == nil || refMarked[issue.ID] {
				continue
			}
			refMarked[issue.ID] = true

			message := fmt.Sprintf(`<a href="%s/commit/%s">%s</a>`, repo.Link(), c.Sha1, html.EscapeString(c.Message))
			if err = CreateRefComment(doer, refRepo, issue, message, c.Sha1); err != nil {
				return err
			}
		}

		// Change issue status only if the commit has been pushed to the default branch.
		// and if the repo is configured to allow only that
		if repo.DefaultBranch != branchName && !repo.CloseIssuesViaCommitInAnyBranch {
			continue
		}
		refMarked = make(map[int64]bool)
		for _, m := range issueCloseKeywordsPat.FindAllStringSubmatch(c.Message, -1) {
			if len(m[3]) == 0 {
				continue
			}
			ref := m[3]

			// issue is from another repo
			if len(m[1]) > 0 && len(m[2]) > 0 {
				refRepo, err = GetRepositoryFromMatch(m[1], m[2])
				if err != nil {
					continue
				}
			} else {
				refRepo = repo
			}

			perm, err := GetUserRepoPermission(refRepo, doer)
			if err != nil {
				return err
			}
			// only close issues in another repo if user has push access
			if perm.CanWrite(UnitTypeCode) {
				if err := changeIssueStatus(refRepo, doer, ref, refMarked, true); err != nil {
					return err
				}
			}
		}

		// It is conflict to have close and reopen at same time, so refsMarked doesn't need to reinit here.
		for _, m := range issueReopenKeywordsPat.FindAllStringSubmatch(c.Message, -1) {
			if len(m[3]) == 0 {
				continue
			}
			ref := m[3]

			// issue is from another repo
			if len(m[1]) > 0 && len(m[2]) > 0 {
				refRepo, err = GetRepositoryFromMatch(m[1], m[2])
				if err != nil {
					continue
				}
			} else {
				refRepo = repo
			}

			perm, err := GetUserRepoPermission(refRepo, doer)
			if err != nil {
				return err
			}

			// only reopen issues in another repo if user has push access
			if perm.CanWrite(UnitTypeCode) {
				if err := changeIssueStatus(refRepo, doer, ref, refMarked, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func transferRepoAction(e Engine, doer, oldOwner *User, repo *Repository) (err error) {
	if err = notifyWatchers(e, &Action{
		ActUserID: doer.ID,
		ActUser:   doer,
		OpType:    ActionTransferRepo,
		RepoID:    repo.ID,
		Repo:      repo,
		IsPrivate: repo.IsPrivate,
		Content:   path.Join(oldOwner.Name, repo.Name),
	}); err != nil {
		return fmt.Errorf("notifyWatchers: %v", err)
	}

	// Remove watch for organization.
	if oldOwner.IsOrganization() {
		if err = watchRepo(e, oldOwner.ID, repo.ID, false); err != nil {
			return fmt.Errorf("watchRepo [false]: %v", err)
		}
	}

	return nil
}

// TransferRepoAction adds new action for transferring repository,
// the Owner field of repository is assumed to be new owner.
func TransferRepoAction(doer, oldOwner *User, repo *Repository) error {
	return transferRepoAction(x, doer, oldOwner, repo)
}

func mergePullRequestAction(e Engine, doer *User, repo *Repository, issue *Issue) error {
	return notifyWatchers(e, &Action{
		ActUserID: doer.ID,
		ActUser:   doer,
		OpType:    ActionMergePullRequest,
		Content:   fmt.Sprintf("%d|%s", issue.Index, issue.Title),
		RepoID:    repo.ID,
		Repo:      repo,
		IsPrivate: repo.IsPrivate,
	})
}

// MergePullRequestAction adds new action for merging pull request.
func MergePullRequestAction(actUser *User, repo *Repository, pull *Issue) error {
	return mergePullRequestAction(x, actUser, repo, pull)
}

func mirrorSyncAction(e Engine, opType ActionType, repo *Repository, refName string, data []byte) error {
	if err := notifyWatchers(e, &Action{
		ActUserID: repo.OwnerID,
		ActUser:   repo.MustOwner(),
		OpType:    opType,
		RepoID:    repo.ID,
		Repo:      repo,
		IsPrivate: repo.IsPrivate,
		RefName:   refName,
		Content:   string(data),
	}); err != nil {
		return fmt.Errorf("notifyWatchers: %v", err)
	}

	defer func() {
		go HookQueue.Add(repo.ID)
	}()

	return nil
}

// MirrorSyncPushActionOptions mirror synchronization action options.
type MirrorSyncPushActionOptions struct {
	RefName     string
	OldCommitID string
	NewCommitID string
	Commits     *PushCommits
}

// MirrorSyncPushAction adds new action for mirror synchronization of pushed commits.
func MirrorSyncPushAction(repo *Repository, opts MirrorSyncPushActionOptions) error {
	if len(opts.Commits.Commits) > setting.UI.FeedMaxCommitNum {
		opts.Commits.Commits = opts.Commits.Commits[:setting.UI.FeedMaxCommitNum]
	}

	apiCommits, err := opts.Commits.ToAPIPayloadCommits(repo.RepoPath(), repo.HTMLURL())
	if err != nil {
		return err
	}

	opts.Commits.CompareURL = repo.ComposeCompareURL(opts.OldCommitID, opts.NewCommitID)
	apiPusher := repo.MustOwner().APIFormat()
	if err := PrepareWebhooks(repo, HookEventPush, &api.PushPayload{
		Ref:        opts.RefName,
		Before:     opts.OldCommitID,
		After:      opts.NewCommitID,
		CompareURL: setting.AppURL + opts.Commits.CompareURL,
		Commits:    apiCommits,
		Repo:       repo.APIFormat(AccessModeOwner),
		Pusher:     apiPusher,
		Sender:     apiPusher,
	}); err != nil {
		return fmt.Errorf("PrepareWebhooks: %v", err)
	}

	data, err := json.Marshal(opts.Commits)
	if err != nil {
		return err
	}

	return mirrorSyncAction(x, ActionMirrorSyncPush, repo, opts.RefName, data)
}

// MirrorSyncCreateAction adds new action for mirror synchronization of new reference.
func MirrorSyncCreateAction(repo *Repository, refName string) error {
	return mirrorSyncAction(x, ActionMirrorSyncCreate, repo, refName, nil)
}

// MirrorSyncDeleteAction adds new action for mirror synchronization of delete reference.
func MirrorSyncDeleteAction(repo *Repository, refName string) error {
	return mirrorSyncAction(x, ActionMirrorSyncDelete, repo, refName, nil)
}

// GetFeedsOptions options for retrieving feeds
type GetFeedsOptions struct {
	RequestedUser    *User
	RequestingUserID int64
	IncludePrivate   bool // include private actions
	OnlyPerformedBy  bool // only actions performed by requested user
	IncludeDeleted   bool // include deleted actions
}

// GetFeeds returns actions according to the provided options
func GetFeeds(opts GetFeedsOptions) ([]*Action, error) {
	cond := builder.NewCond()

	var repoIDs []int64
	if opts.RequestedUser.IsOrganization() {
		env, err := opts.RequestedUser.AccessibleReposEnv(opts.RequestingUserID)
		if err != nil {
			return nil, fmt.Errorf("AccessibleReposEnv: %v", err)
		}
		if repoIDs, err = env.RepoIDs(1, opts.RequestedUser.NumRepos); err != nil {
			return nil, fmt.Errorf("GetUserRepositories: %v", err)
		}

		cond = cond.And(builder.In("repo_id", repoIDs))
	}

	cond = cond.And(builder.Eq{"user_id": opts.RequestedUser.ID})

	if opts.OnlyPerformedBy {
		cond = cond.And(builder.Eq{"act_user_id": opts.RequestedUser.ID})
	}
	if !opts.IncludePrivate {
		cond = cond.And(builder.Eq{"is_private": false})
	}

	if !opts.IncludeDeleted {
		cond = cond.And(builder.Eq{"is_deleted": false})
	}

	actions := make([]*Action, 0, 20)

	if err := x.Limit(20).Desc("id").Where(cond).Find(&actions); err != nil {
		return nil, fmt.Errorf("Find: %v", err)
	}

	if err := ActionList(actions).LoadAttributes(); err != nil {
		return nil, fmt.Errorf("LoadAttributes: %v", err)
	}

	return actions, nil
}
