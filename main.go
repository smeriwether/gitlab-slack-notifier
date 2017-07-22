package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/nlopes/slack"
	"github.com/rs/cors"
	gitlab "github.com/xanzy/go-gitlab"
)

var (
	secretToken  string
	botName      string
	users        *[]User
	activeUsers  *[]User
	slackClient  SlackReadWriter
	gitlabClient GitlabReader
)

func main() {
	log.Println("Listening for Gitlab events")
	defer log.Println("Stopping...")

	secretToken = os.Getenv("SECRET_TOKEN")
	botName = os.Getenv("BOT_NAME")
	slackToken := os.Getenv("SLACK_TOKEN")
	gitlabToken := os.Getenv("GITLAB_TOKEN")
	activeUsernames := os.Getenv("ACTIVE_USERS")
	sslKey := os.Getenv("SSL_KEY_PATH")
	sslCert := os.Getenv("SSL_CERT_PATH")

	if slackClient == nil {
		if slackToken == "" {
			panic("SLACK_TOKEN must not be empty")
		}
		slackClient = NewSlackClient(slackToken)
	}

	if gitlabClient == nil {
		if gitlabToken == "" {
			panic("GITLAB_TOKEN must not be empty")
		}
		gitlabClient = NewGitlabClient(gitlabToken)
	}

	if activeUsers == nil {
		usernames := strings.Split(activeUsernames, ",")
		var internalActiveUsernames []User
		for _, username := range usernames {
			if username == "" {
				continue
			}
			internalActiveUsernames = append(internalActiveUsernames, User{GitlabUsername: username})
		}
		activeUsers = &internalActiveUsernames
		if activeUsers == nil || len(*activeUsers) == 0 {
			panic("ACTIVE_USERS must not be empty")
		}
	}

	// Every so often we should double check the gitlab & slack users
	ticker := time.NewTicker(time.Minute * 180)
	defer ticker.Stop()
	go func() {
		go populateUsers()
		for range ticker.C {
			go populateUsers()
		}
	}()

	useSSL := true
	if _, err := os.Stat(sslKey); os.IsNotExist(err) {
		log.Println("Unable to find ssl key")
		useSSL = false
	}
	if _, err := os.Stat(sslCert); os.IsNotExist(err) {
		log.Println("Unable to find ssl certificate")
		useSSL = false
	}

	r := mux.NewRouter()
	r.HandleFunc("/comments", CommentWebhookHandler).Methods("POST")
	r.HandleFunc("/pipeline", PipelineWebhookHandler).Methods("POST")
	r.HandleFunc("/healthz", HealthzHandler).Methods("GET")

	loggingHandler := handlers.LoggingHandler(os.Stderr, r)
	authHandler := AuthHandler{loggingHandler}
	errorHandler := ErrorHandler{authHandler}

	handler := http.NewServeMux()
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedHeaders: []string{"accept", "x-csrf-token"},
	})
	handler.Handle("/", c.Handler(errorHandler))

	tlsServer := &http.Server{
		Handler:      handler,
		Addr:         ":9090",
		WriteTimeout: 30 * time.Second,
		ReadTimeout:  30 * time.Second,
		ErrorLog:     log.New(ioutil.Discard, "Debug: ", log.Ldate|log.Ltime),
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	go func() {
		<-sig
		ticker.Stop()
		if err := tlsServer.Close(); err != nil {
			log.Println("Error closing server", err)
		}
		log.Println("Exiting...")
		os.Exit(1)

	}()

	log.Println("Listening for requests on :9090...")
	if useSSL {
		log.Fatal(tlsServer.ListenAndServeTLS(sslCert, sslKey))
	} else {
		log.Fatal(tlsServer.ListenAndServe())
	}
}

func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func PipelineWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Body == nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("Body must not be empty")); err != nil {
			log.Println(err)
		}
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Println("Error closing body:", err)
		}
	}()

	var root RootRequest
	if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		if _, err := w.Write([]byte(fmt.Sprintf("JSON decoding error: %v", err))); err != nil {
			log.Println(err)
		}
		return
	}

	// This handler *should* only receive pipeline updates but we need to still reject everything else.
	if !root.PipelineRequest() || !root.Valid() || root.Commit == nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("Not valid or not a pipline request")); err != nil {
			log.Println(err)
		}
		return
	}

	if !root.FailedPipeline() {
		w.WriteHeader(http.StatusOK)
		return
	}

	codeAuthor, _ := discoverUsers(&root)
	if codeAuthor == nil {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("User discovery error")); err != nil {
			log.Println(err)
		}
		return
	}

	// Don't send message if the receiver (codeAuthor) is not an active user
	if !activeUser(codeAuthor) {
		w.WriteHeader(http.StatusOK)
		return
	}

	message := fmt.Sprintf("Pipeline failed for your <%s|Commit>", root.Commit.URL)
	if root.Project != nil && root.ObjectAttributes.Ref != nil {
		message += fmt.Sprintf(" (%s/%s)", root.Project.Name, *root.ObjectAttributes.Ref)
	}
	go slackClient.PostMessage(codeAuthor.SlackID, message, "")

	w.WriteHeader(http.StatusOK)
}

func CommentWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Body == nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("Body must not be empty")); err != nil {
			log.Println(err)
		}
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Println("Error closing body:", err)
		}
	}()

	var root RootRequest
	if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println(err)
		if _, err := w.Write([]byte(fmt.Sprintf("JSON decoding error: %v", err))); err != nil {
			log.Println(err)
		}
		return
	}

	// This handler *should* only receive comment updates but we need to still reject everything else.
	if !root.CommentRequest() || !root.Valid() {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("Not valid or not a comment request")); err != nil {
			log.Println(err)
		}
		return
	}

	codeAuthor, commentAuthor := discoverUsers(&root)
	if codeAuthor == nil || commentAuthor == nil {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("User discovery error")); err != nil {
			log.Println(err)
		}
		return
	}

	log.Printf("%s made a comment on %s MR\n", commentAuthor.GitlabUsername, codeAuthor.GitlabUsername)

	// Don't send message if the receiver (codeAuthor) is not an active user
	// Don't send message if the codeAuthor & commentAuthor are the same person (that got annoying)
	if !activeUser(codeAuthor) || codeAuthor.Same(commentAuthor) {
		log.Println("Ignoring the comment")
		log.Printf("User is not active: %v\n", !activeUser(codeAuthor))
		log.Printf("Code author is also the comment author: %v\n", codeAuthor.Same(commentAuthor))
		w.WriteHeader(http.StatusOK)
		return
	}

	message := fmt.Sprintf(
		"%s made a comment on your <%s|Merge Request>",
		commentAuthor.GitlabUsername, root.ObjectAttributes.URL,
	)

	log.Println("Sending slack message")
	log.Println(message)

	go slackClient.PostMessage(codeAuthor.SlackID, message, root.ObjectAttributes.Note)

	w.WriteHeader(http.StatusOK)
}

func discoverUsers(root *RootRequest) (*User, *User) {
	var codeAuthor User
	var commentAuthor User

	if users != nil {
		for _, user := range *users {
			if root.ObjectAttributes.AuthorID == user.GitlabID {
				commentAuthor = user
			}

			if root.MergeRequest != nil {
				if root.MergeRequest.AuthorID == user.GitlabID {
					codeAuthor = user
				}
			} else if root.Commit != nil {
				if root.Commit.Author.Email == user.Email {
					codeAuthor = user
				}
			}

		}

		return &codeAuthor, &commentAuthor
	}

	return nil, nil
}

func activeUser(user *User) bool {
	if activeUsers != nil {
		for _, u := range *activeUsers {
			if u.GitlabUsername == user.GitlabUsername {
				return true
			}
		}
	}

	return false
}

// TODO: This can be done in parallel
func populateUsers() {
	log.Println("Populating users...")
	defer log.Println("Done populating users")

	slackUsers, err := slackClient.ListUsers()
	if err != nil || slackUsers == nil {
		log.Println(err)
		return
	}
	gitlabUsers, err := gitlabClient.ListUsers()
	if err != nil || gitlabUsers == nil {
		log.Println(err)
		return
	}

	var internalUsers []User
	for _, gu := range *gitlabUsers {
		for _, su := range *slackUsers {
			if gu.Email == su.Email {
				internalUsers = append(internalUsers, User{
					Email:          gu.Email,
					SlackID:        su.SlackID,
					SlackUsername:  su.SlackUsername,
					GitlabID:       gu.GitlabID,
					GitlabUsername: gu.GitlabUsername,
				})
				break
			}
		}
	}

	log.Println("Found users:", internalUsers)
	users = &internalUsers
}

// Internal Stuff

type User struct {
	Email          string
	SlackID        string
	SlackUsername  string
	GitlabID       int
	GitlabUsername string
}

func (u *User) Same(user *User) bool {
	return u.Email == user.Email ||
		u.SlackID == user.SlackID ||
		u.SlackUsername == user.SlackUsername ||
		u.GitlabID == user.GitlabID ||
		u.GitlabUsername == user.GitlabUsername
}

// Gitlab Stuff

type RootRequest struct {
	ObjectKind       string                   `json:"object_kind"`
	User             *UserRequest             `json:"user"`
	ProjectID        int                      `json:"project_id"`
	Project          *ProjectRequest          `json:"project"`
	Repository       *RepositoryRequest       `json:"repository"`
	ObjectAttributes *ObjectAttributesRequest `json:"object_attributes"`
	MergeRequest     *MergeRequestRequest     `json:"merge_request"`
	Commit           *CommitRequest           `json:"commit"`
	Builds           *[]BuildRequest          `json:"builds"`
}

func (root *RootRequest) Valid() bool {
	return root.ObjectAttributes != nil && (root.MergeRequest != nil || root.Commit != nil)
}

func (root *RootRequest) CommentRequest() bool {
	return root.ObjectKind == "note"
}

func (root *RootRequest) PipelineRequest() bool {
	return root.ObjectKind == "pipeline"
}

func (root *RootRequest) FailedPipeline() bool {
	return root.ObjectAttributes.Status != nil && *root.ObjectAttributes.Status == "failed"
}

type UserRequest struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
}

type ProjectRequest struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	WebURL            string  `json:"web_url"`
	AvatarURL         *string `json:"avatar_url"`
	GitSSHURL         string  `json:"git_ssh_url"`
	GitHTTPURL        string  `json:"git_http_url"`
	Namespace         string  `json:"namespace"`
	VisibilityLevel   int     `json:"visibility_level"`
	PathWithNamespace string  `json:"path_with_namespace"`
	DefaultBranch     string  `json:"default_branch"`
	Homepage          string  `json:"homepage"`
	URL               string  `json:"url"`
	SSHURL            string  `json:"ssh_url"`
	HTTPURL           string  `json:"http_url"`
}

type RepositoryRequest struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
}

type ObjectAttributesRequest struct {
	ID           int            `json:"id"`
	Note         string         `json:"note"`
	NoteableType string         `json:"noteable_type"`
	AuthorID     int            `json:"author_id"`
	CreatedAt    string         `json"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	ProjectID    int            `json:"project_id"`
	Attachment   *string        `json:"attachment"`
	LineCode     *string        `json:"line_code"`
	CommitID     string         `json:"commit_id"`
	NoteableID   *int           `json:"noteable_id"`
	System       bool           `json:"system"`
	StDiff       *StDiffRequest `json:"st_diff"`
	URL          string         `json:"url"`
	Status       *string        `json:"status"`
	Stages       *[]string      `json:"stages"`
	FinishedAt   *string        `json:"finished_at"`
	Duration     *int           `json:"duration"`
	Ref          *string        `json:"ref"`
}

type StDiffRequest struct {
	Diff        string `json:"diff"`
	NewPath     string `json:"new_path"`
	OldPath     string `json:"old_path"`
	AMode       string `json:"a_mode"`
	BMode       string `json:"b_mode"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
}

type MergeRequestRequest struct {
	ID              int             `json:"id"`
	TargetBranch    string          `json:"target_branch"`
	SourceBranch    string          `json:"source_branch"`
	SourceProjectID int             `json:"source_project_id"`
	AuthorID        int             `json:"author_id"`
	AssigneeID      int             `json:"assignee_id"`
	Title           string          `json:"title"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json"updated_at"`
	MilestoneID     int             `json:"milestone_id"`
	State           string          `json:"state"`
	MergeStatus     string          `json:"merge_status"`
	TargetProjectID int             `json:"target_project_id"`
	IID             int             `json:"iid"`
	Description     string          `json:"description"`
	Position        int             `json:"position"`
	LockedAt        *string         `json:"locked_at"`
	Source          *ProjectRequest `json:"source"`
	Target          *ProjectRequest `json:"target"`
	LastCommit      *CommitRequest  `json:"last_commit"`
	WorkInProgress  bool            `json:"work_in_progress"`
	Assignee        *UserRequest    `json:"assignee"`
}

type CommitRequest struct {
	ID        string         `json:"id"`
	Message   string         `json:"message"`
	Timestamp string         `json:"timestamp"`
	URL       string         `json:"url"`
	Author    *AuthorRequest `json:"author"`
}

type AuthorRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type BuildRequest struct {
	ID         int          `json:"id"`
	Stage      string       `json:"stage"`
	Name       string       `json:"name"`
	Status     string       `json:"status"`
	CreatedAt  string       `json:"created_at"`
	StartedAt  string       `json:"started_at"`
	FinishedAt string       `json:"finished_at"`
	When       string       `json:"when"`
	Manual     bool         `json:"manual"`
	User       *UserRequest `json:"user"`
}

type GitlabReader interface {
	ListUsers() (*[]User, error)
}

type GitlabClient struct {
	client *gitlab.Client
}

func (client *GitlabClient) ListUsers() (*[]User, error) {
	active := true
	gitlabUsers, _, err := client.client.Users.ListUsers(&gitlab.ListUsersOptions{Active: &active})
	if err != nil {
		return nil, err
	}

	var users []User
	for _, u := range gitlabUsers {
		users = append(users, User{
			Email:          u.Email,
			GitlabID:       u.ID,
			GitlabUsername: u.Username,
		})
	}

	return &users, nil
}

func NewGitlabClient(token string) *GitlabClient {
	git := gitlab.NewClient(nil, token)
	if err := git.SetBaseURL("https://gitlab.molecule.io/api/v3/"); err != nil {
		panic(err)
	}

	return &GitlabClient{git}
}

// Slack Stuff

type SlackReadWriter interface {
	PostMessage(channel, message, attachment string)
	ListUsers() (*[]User, error)
}

type SlackClient struct {
	client *slack.Client
}

func (client *SlackClient) PostMessage(channel, message, attachment string) {
	var attachments []slack.Attachment
	if attachment != "" {
		attachments = append(attachments, slack.Attachment{Text: attachment})
	}

	_, _, err := client.client.PostMessage(
		channel, message,
		slack.PostMessageParameters{
			Username:    botName,
			AsUser:      true,
			Attachments: attachments,
		},
	)
	if err != nil {
		log.Println(err)
	}
}

func (client *SlackClient) ListUsers() (*[]User, error) {
	slackUsers, err := client.client.GetUsers()
	if err != nil {
		return nil, err
	}

	var users []User
	for _, u := range slackUsers {
		users = append(users, User{
			Email:         u.Profile.Email,
			SlackID:       u.ID,
			SlackUsername: u.Name,
		})
	}

	return &users, nil
}

func NewSlackClient(token string) *SlackClient {
	return &SlackClient{slack.New(token)}
}

// HTTP Middleware

type AuthHandler struct {
	handler http.Handler
}

// Gitlab will send a secret token in the header of the response that we can use to verify the request.
func (h AuthHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Short circuit if just asking about health
	if req.URL != nil && req.URL.Path == "/healthz" {
		h.handler.ServeHTTP(w, req)
		return
	}

	if token, ok := req.Header["X-Gitlab-Token"]; !ok || token[0] != secretToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	h.handler.ServeHTTP(w, req)
}

type ErrorHandler struct {
	handler http.Handler
}

func (h ErrorHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	responseWriter := MyAwesomeResponseWriter{ResponseWriter: w}
	h.handler.ServeHTTP(&responseWriter, req)
	if responseWriter.Error() {
		log.Printf("Returning status code %d\n", responseWriter.StatusCode)
		log.Printf("Error: %s\n", responseWriter.Text)
	}
}

type MyAwesomeResponseWriter struct {
	StatusCode     int
	Text           string
	ResponseWriter http.ResponseWriter
}

func (w MyAwesomeResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *MyAwesomeResponseWriter) Write(bytes []byte) (int, error) {
	w.Text = string(bytes)
	return w.ResponseWriter.Write(bytes)
}

func (w *MyAwesomeResponseWriter) WriteHeader(statusCode int) {
	w.StatusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *MyAwesomeResponseWriter) Error() bool {
	return w.StatusCode > 399
}
