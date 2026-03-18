package dashboardapi

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
)

func newMultipartRequest(t *testing.T, fields map[string][]string, files []multipartFilePayload) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatalf("WriteField(%s): %v", name, err)
			}
		}
	}
	for _, file := range files {
		var (
			part io.Writer
			err  error
		)
		if strings.TrimSpace(file.ContentType) != "" {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", `form-data; name="`+file.FieldName+`"; filename="`+file.Filename+`"`)
			header.Set("Content-Type", file.ContentType)
			part, err = writer.CreatePart(header)
		} else {
			part, err = writer.CreateFormFile(file.FieldName, file.Filename)
		}
		if err != nil {
			t.Fatalf("CreatePart(%s): %v", file.FieldName, err)
		}
		if _, err := part.Write(file.Content); err != nil {
			t.Fatalf("part.Write(%s): %v", file.Filename, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, httptest.NewRecorder()
}

func TestReadIssueImageUploadHelper(t *testing.T) {
	req, _ := newMultipartRequest(t, map[string][]string{
		"ignored": {"field"},
	}, []multipartFilePayload{{
		FieldName: "file",
		Filename:  "diagram.png",
		Content:   []byte("png payload"),
	}})
	file, filename, err := readIssueImageUpload(req)
	if err != nil {
		t.Fatalf("readIssueImageUpload: %v", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll uploaded file: %v", err)
	}
	_ = file.Close()
	if filename != "diagram.png" || string(data) != "png payload" {
		t.Fatalf("unexpected upload payload filename=%q data=%q", filename, string(data))
	}

	reqMissing, _ := newMultipartRequest(t, map[string][]string{
		"body": {"no file"},
	}, nil)
	if _, _, err := readIssueImageUpload(reqMissing); err == nil || !strings.Contains(err.Error(), "file is required") {
		t.Fatalf("expected missing file error, got %v", err)
	}

	reqBlankName, _ := newMultipartRequest(t, nil, []multipartFilePayload{{
		FieldName: "file",
		Filename:  "",
		Content:   []byte("payload"),
	}})
	if _, _, err := readIssueImageUpload(reqBlankName); err == nil || !strings.Contains(err.Error(), "file is required") {
		t.Fatalf("expected blank filename error, got %v", err)
	}
}

func TestReadIssueCommentMultipartHelper(t *testing.T) {
	req, rec := newMultipartRequest(t, map[string][]string{
		"body":                  {"Comment body"},
		"parent_comment_id":     {"cmt_parent"},
		"remove_attachment_ids": {"att_keep", "", "att_drop"},
		"ignored":               {"value"},
	}, []multipartFilePayload{{
		FieldName:   "files",
		Filename:    "note.txt",
		ContentType: "text/plain",
		Content:     []byte("attachment body"),
	}})

	input, cleanup, err := readIssueCommentMultipart(rec, req, "UI")
	if err != nil {
		t.Fatalf("readIssueCommentMultipart: %v", err)
	}
	if input.Author.Name != "UI" || input.Author.Type != "source" {
		t.Fatalf("unexpected author: %#v", input.Author)
	}
	if input.Body == nil || *input.Body != "Comment body" {
		t.Fatalf("unexpected body: %#v", input.Body)
	}
	if input.ParentCommentID != "cmt_parent" {
		t.Fatalf("unexpected parent comment id: %q", input.ParentCommentID)
	}
	if len(input.RemoveAttachmentIDs) != 2 || input.RemoveAttachmentIDs[0] != "att_keep" || input.RemoveAttachmentIDs[1] != "att_drop" {
		t.Fatalf("unexpected remove_attachment_ids: %#v", input.RemoveAttachmentIDs)
	}
	if len(input.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", input.Attachments)
	}
	data, err := os.ReadFile(input.Attachments[0].Path)
	if err != nil {
		t.Fatalf("ReadFile temp attachment: %v", err)
	}
	if string(data) != "attachment body" || input.Attachments[0].ContentType != "text/plain" {
		t.Fatalf("unexpected temp attachment: content=%q meta=%#v", string(data), input.Attachments[0])
	}
	cleanup()
	if _, err := os.Stat(input.Attachments[0].Path); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove attachment path, stat err=%v", err)
	}

	reqNoBody, recNoBody := newMultipartRequest(t, nil, []multipartFilePayload{{
		FieldName:   "files",
		Filename:    "only-file.txt",
		ContentType: "text/plain",
		Content:     []byte("attachment only"),
	}})
	noBodyInput, noBodyCleanup, err := readIssueCommentMultipart(recNoBody, reqNoBody, "CLI")
	if err != nil {
		t.Fatalf("readIssueCommentMultipart no body: %v", err)
	}
	defer noBodyCleanup()
	if noBodyInput.Body != nil {
		t.Fatalf("expected nil body when multipart omits body field, got %#v", noBodyInput.Body)
	}
	if noBodyInput.Author.Name != "CLI" {
		t.Fatalf("unexpected author for no-body input: %#v", noBodyInput.Author)
	}

	reqInvalid := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("not multipart"))
	reqInvalid.Header.Set("Content-Type", "text/plain")
	if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), reqInvalid, "UI"); err == nil {
		cleanup()
		t.Fatal("expected multipart parsing error")
	}
}

func TestIssueCommentAndImageRoutesRejectInvalidMethods(t *testing.T) {
	store, srv := setupDashboardServerTest(t, testProvider{})

	issue, err := store.CreateIssue("", "", "Route coverage", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	createResp := requestMultipartForm(t, srv, http.MethodPost, "/api/v1/app/issues/"+issue.Identifier+"/comments", map[string][]string{
		"body": {"Created comment"},
	}, nil)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create comment expected 201, got %d", createResp.StatusCode)
	}
	commentID := decodeResponse(t, createResp)["id"].(string)

	putComments := requestJSON(t, srv, http.MethodPut, "/api/v1/app/issues/"+issue.Identifier+"/comments", nil)
	if putComments.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("comments collection expected 405, got %d", putComments.StatusCode)
	}
	_ = putComments.Body.Close()

	getCommentItem := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/comments/"+commentID, nil)
	if getCommentItem.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("comment item GET expected 405, got %d", getCommentItem.StatusCode)
	}
	_ = getCommentItem.Body.Close()

	putImages := requestJSON(t, srv, http.MethodPut, "/api/v1/app/issues/"+issue.Identifier+"/images", nil)
	if putImages.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("images collection expected 405, got %d", putImages.StatusCode)
	}
	_ = putImages.Body.Close()

	missingImage := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/images/missing/content", nil)
	if missingImage.StatusCode != http.StatusNotFound {
		t.Fatalf("missing image content expected 404, got %d", missingImage.StatusCode)
	}
	_ = decodeResponse(t, missingImage)
}
