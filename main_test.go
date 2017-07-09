package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	users = &[]User{
		{
			Email:          "stephen1@molecule.io",
			SlackID:        "SLACKID1",
			SlackUsername:  "smeriwether1",
			GitlabID:       1,
			GitlabUsername: "smeriwether1",
		},
		{
			Email:          "stephen2@molecule.io",
			SlackID:        "SLACKID2",
			SlackUsername:  "smeriwether2",
			GitlabID:       2,
			GitlabUsername: "smeriwether2",
		},
	}
	activeUsers = users

	os.Exit(m.Run())
}

func TestCommentWebhookHandlerFromMergeRequestComment(t *testing.T) {
	handler := http.HandlerFunc(CommentWebhookHandler)
	req, err := http.NewRequest("POST", "/comments", bytes.NewBuffer(MergeRequestCommentRequest()))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	slackStub := slackClientStub{}
	slackClient = &slackStub

	handler.ServeHTTP(rr, req)
	time.Sleep(1 * time.Second) // Sleep to let goroutines finish, this is a code smell :(

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	if slackStub.receivedChannel != "SLACKID2" {
		t.Errorf("slack client received wrong channel: got %v want %v",
			slackStub.receivedChannel, "SLACKID2")
	}

	if !strings.Contains(slackStub.receivedMessage, "smeriwether1 made a comment") {
		t.Errorf("slack client received wrong message: got %v wanted to include %v",
			slackStub.receivedMessage, "smeriwether1 made a comment")
	}
}

func TestCommentWebhookHandlerFromCommitComment(t *testing.T) {
	handler := http.HandlerFunc(CommentWebhookHandler)
	req, err := http.NewRequest("POST", "/comments", bytes.NewBuffer(CommitCommentRequest()))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	slackStub := slackClientStub{}
	slackClient = &slackStub

	handler.ServeHTTP(rr, req)
	time.Sleep(1 * time.Second) // Sleep to let goroutines finish, this is a code smell :(

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	if slackStub.receivedChannel != "SLACKID1" {
		t.Errorf("slack client received wrong channel: got %v want %v",
			slackStub.receivedChannel, "SLACKID1")
	}

	if !strings.Contains(slackStub.receivedMessage, "smeriwether2 made a comment") {
		t.Errorf("slack client received wrong message: got %v wanted to include %v",
			slackStub.receivedMessage, "smeriwether2 made a comment")
	}
}

func TestCommentWebhookHandlerWithInactiveUser(t *testing.T) {
	activeUsers = &[]User{
		{GitlabUsername: "smeriwether1"},
	}
	handler := http.HandlerFunc(CommentWebhookHandler)
	req, err := http.NewRequest("POST", "/comments", bytes.NewBuffer(MergeRequestCommentRequest()))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	slackStub := slackClientStub{}
	slackClient = &slackStub

	handler.ServeHTTP(rr, req)
	time.Sleep(1 * time.Second) // Sleep to let goroutines finish, this is a code smell :(

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// We don't want to send any slack messages
	if slackStub.receivedChannel == "SLACKID1" {
		t.Errorf("slack client received wrong channel: got %v want (empty)", slackStub.receivedChannel)
	}

	if slackStub.receivedMessage != "" {
		t.Errorf("slack client received wrong message: got %v want (empty)", slackStub.receivedMessage)
	}

}

type slackClientStub struct {
	receivedChannel    string
	receivedMessage    string
	receivedAttachment string
}

func (stub *slackClientStub) PostMessage(channel, message, attachment string) {
	stub.receivedChannel = channel
	stub.receivedMessage = message
	stub.receivedAttachment = attachment
}

func (stub *slackClientStub) ListUsers() (*[]User, error) {
	return nil, nil
}

func MergeRequestCommentRequest() []byte {
	return []byte(
		`
		{
			"object_kind": "note",
			"user": {
				"name": "Administrator",
				"username": "root",
				"avatar_url": "http://www.gravatar.com/avatar/e64c7d89f26bd1972efa854d13d7d=40\u0026d=identicon"
			},
			"project_id": 10,
			"project":{
				"name":"Gitlab Test",
				"description":"Aut reprehenderit ut est.",
				"web_url":"http://example.com/gitlab-org/gitlab-test",
				"avatar_url":null,
				"git_ssh_url":"git@example.com:gitlab-org/gitlab-test.git",
				"git_http_url":"http://example.com/gitlab-org/gitlab-test.git",
				"namespace":"Gitlab Org",
				"visibility_level":10,
				"path_with_namespace":"gitlab-org/gitlab-test",
				"default_branch":"master",
				"homepage":"http://example.com/gitlab-org/gitlab-test",
				"url":"http://example.com/gitlab-org/gitlab-test.git",
				"ssh_url":"git@example.com:gitlab-org/gitlab-test.git",
				"http_url":"http://example.com/gitlab-org/gitlab-test.git"
			},
			"repository":{
				"name": "Gitlab Test",
				"url": "http://localhost/gitlab-org/gitlab-test.git",
				"description": "Aut reprehenderit ut est.",
				"homepage": "http://example.com/gitlab-org/gitlab-test"
			},
			"object_attributes": {
				"id": 1244,
				"note": "This MR needs work.",
				"noteable_type": "MergeRequest",
				"author_id": 1,
				"created_at": "2015-05-17 18:21:36 UTC",
				"updated_at": "2015-05-17 18:21:36 UTC",
				"project_id": 10,
				"attachment": null,
				"line_code": null,
				"commit_id": "",
				"noteable_id": 7,
				"system": false,
				"st_diff": null,
				"url": "http://example.com/gitlab-org/gitlab-test/merge_requests/1#note_1244"
			},
			"merge_request": {
				"id": 100,
				"target_branch": "markdown",
				"source_branch": "master",
				"source_project_id": 10,
				"author_id": 2,
				"assignee_id": 1,
				"title": "Tempora et eos debitis quae laborum et.",
				"created_at": "2015-03-01 20:12:53 UTC",
				"updated_at": "2015-03-21 18:27:27 UTC",
				"milestone_id": 11,
				"state": "opened",
				"merge_status": "cannot_be_merged",
				"target_project_id": 10,
				"iid": 1,
				"description": "Et voluptas corrupti assumenda temporibus. Architecto cum...",
				"position": 0,
				"locked_at": null,
				"source": {
					"name": "Gitlab Test",
					"description": "Aut reprehenderit ut est.",
					"web_url": "http://example.com/gitlab-org/gitlab-test",
					"avatar_url": null,
					"git_ssh_url": "git@example.com:gitlab-org/gitlab-test.git",
					"git_http_url": "http://example.com/gitlab-org/gitlab-test.git",
					"namespace": "Gitlab Org",
					"visibility_level": 10,
					"path_with_namespace": "gitlab-org/gitlab-test",
					"default_branch": "master",
					"homepage": "http://example.com/gitlab-org/gitlab-test",
					"url": "http://example.com/gitlab-org/gitlab-test.git",
					"ssh_url": "git@example.com:gitlab-org/gitlab-test.git",
					"http_url": "http://example.com/gitlab-org/gitlab-test.git"
				},
				"target": {
					"name": "Gitlab Test",
					"description": "Aut reprehenderit ut est.",
					"web_url": "http://example.com/gitlab-org/gitlab-test",
					"avatar_url": null,
					"git_ssh_url": "git@example.com:gitlab-org/gitlab-test.git",
					"git_http_url": "http://example.com/gitlab-org/gitlab-test.git",
					"namespace": "Gitlab Org",
					"visibility_level": 10,
					"path_with_namespace": "gitlab-org/gitlab-test",
					"default_branch": "master",
					"homepage": "http://example.com/gitlab-org/gitlab-test",
					"url": "http://example.com/gitlab-org/gitlab-test.git",
					"ssh_url": "git@example.com:gitlab-org/gitlab-test.git",
					"http_url": "http://example.com/gitlab-org/gitlab-test.git"
				},
				"last_commit": {
					"id": "562e173be03b8ff2efb05345d12df18815438a4b",
					"message": "Merge branch 'another-branch' into 'master'\n\nCheck in this test\n",
					"timestamp": "2015-04-08T21: 00:25-07:00",
					"url": "http://example.com/gitlab-org/gitlab-test/commit/562e173be03b8ff2efb05345d12df188154",
					"author": {
						"name": "Stephen 1 Meriwether",
						"email": "stephen1@molecule.io"
					}
				},
				"work_in_progress": false,
				"assignee": {
					"name": "Stephen 2 Meriwether",
					"username": "smeriwether2",
					"avatar_url": "http://www.gravatar.com/avatar/e64c7d89f26bd1972efa3d7d=40\u0026d=identicon"
				}
			}
		}
	`,
	)
}

func CommitCommentRequest() []byte {
	return []byte(
		`
	{
		"object_kind": "note",
		"user": {
			"name": "Administrator",
			"username": "root",
			"avatar_url": "http://www.gravatar.com/avatar/e64c7d89f26bd1972efa854d13d7d?s=40\u0026d=identicon"
		},
		"project_id": 10,
		"project":{
			"name":"Gitlab Test",
			"description":"Aut reprehenderit ut est.",
			"web_url":"http://example.com/gitlabhq/gitlab-test",
			"avatar_url":null,
			"git_ssh_url":"git@example.com:gitlabhq/gitlab-test.git",
			"git_http_url":"http://example.com/gitlabhq/gitlab-test.git",
			"namespace":"GitlabHQ",
			"visibility_level":20,
			"path_with_namespace":"gitlabhq/gitlab-test",
			"default_branch":"master",
			"homepage":"http://example.com/gitlabhq/gitlab-test",
			"url":"http://example.com/gitlabhq/gitlab-test.git",
			"ssh_url":"git@example.com:gitlabhq/gitlab-test.git",
			"http_url":"http://example.com/gitlabhq/gitlab-test.git"
		},
		"repository":{
			"name": "Gitlab Test",
			"url": "http://example.com/gitlab-org/gitlab-test.git",
			"description": "Aut reprehenderit ut est.",
			"homepage": "http://example.com/gitlab-org/gitlab-test"
		},
		"object_attributes": {
			"id": 1243,
			"note": "This is a commit comment. How does this work?",
			"noteable_type": "Commit",
			"author_id": 2,
			"created_at": "2015-05-17 18:08:09 UTC",
			"updated_at": "2015-05-17 18:08:09 UTC",
			"project_id": 10,
			"attachment":null,
			"line_code": "bec9703f7a456cd2b4ab5fb3220ae016e3e394e3_0_1",
			"commit_id": "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
			"noteable_id": null,
			"system": false,
			"st_diff": {
			"diff": "--- /dev/null\n+++ b/six\n@@ -0,0 +1 @@\n+Subproject commit 409f37c4f05865e4fb208c77\n",
			"new_path": "six",
			"old_path": "six",
			"a_mode": "0",
			"b_mode": "160000",
			"new_file": true,
			"renamed_file": false,
			"deleted_file": false
			},
			"url": "http://example.com/gitlab-org/gitlab-test/commit/cfe32cf61b73a0d5e9f13e774abde7#note_1243"
		},
		"commit": {
			"id": "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
			"message": "Signed-off-by: Stephen \u003cstephen1@molecule.io\u003e\n",
			"timestamp": "2014-02-27T10:06:20+02:00",
			"url": "http://example.com/gitlab-org/gitlab-test/commit/cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
			"author": {
			"name": "Stephen 1 Meriwether",
			"email": "stephen1@molecule.io"
			}
		}
	}
	`,
	)
}
