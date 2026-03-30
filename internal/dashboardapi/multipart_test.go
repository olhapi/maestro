package dashboardapi

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
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

func buildMultipartBody(t *testing.T, fields map[string][]string, files []multipartFilePayload) (string, []byte) {
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

	return writer.FormDataContentType(), body.Bytes()
}

type failingReader struct {
	data      []byte
	failAfter int
	pos       int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if r.failAfter >= 0 && r.pos >= r.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	limit := len(r.data)
	if r.failAfter >= 0 && r.failAfter < limit {
		limit = r.failAfter
	}
	n := copy(p, r.data[r.pos:limit])
	r.pos += n
	if r.failAfter >= 0 && r.pos >= r.failAfter {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func TestReadIssueAssetUploadHelper(t *testing.T) {
	req, _ := newMultipartRequest(t, map[string][]string{
		"ignored": {"field"},
	}, []multipartFilePayload{{
		FieldName: "file",
		Filename:  "diagram.png",
		Content:   []byte("png payload"),
	}})
	file, filename, err := readIssueAssetUpload(req)
	if err != nil {
		t.Fatalf("readIssueAssetUpload: %v", err)
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
	if _, _, err := readIssueAssetUpload(reqMissing); err == nil || !strings.Contains(err.Error(), "file is required") {
		t.Fatalf("expected missing file error, got %v", err)
	}

	reqBlankName, _ := newMultipartRequest(t, nil, []multipartFilePayload{{
		FieldName: "file",
		Filename:  "",
		Content:   []byte("payload"),
	}})
	if _, _, err := readIssueAssetUpload(reqBlankName); err == nil || !strings.Contains(err.Error(), "file is required") {
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

func TestReadIssueCommentMultipartErrorBranches(t *testing.T) {
	t.Run("missing boundary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("plain text"))
		req.Header.Set("Content-Type", "multipart/form-data")
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected missing multipart boundary to fail")
		}
	})

	t.Run("temp dir failure", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "tmpdir-file")
		if err := os.WriteFile(tmpFile, []byte("tmp"), 0o644); err != nil {
			t.Fatalf("WriteFile tmp file: %v", err)
		}
		t.Setenv("TMPDIR", tmpFile)
		contentType, raw := buildMultipartBody(t, map[string][]string{
			"body": {"Comment body"},
		}, nil)
		req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(raw))
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected temp dir creation to fail")
		}
	})

	t.Run("body read error", func(t *testing.T) {
		contentType, raw := buildMultipartBody(t, map[string][]string{
			"body": {"Comment body with enough data to trip the reader"},
		}, nil)
		cutoff := bytes.Index(raw, []byte("Comment body with enough data to trip the reader")) + 10
		req := httptest.NewRequest(http.MethodPost, "/upload", &failingReader{data: raw, failAfter: cutoff})
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected body read error")
		}
	})

	t.Run("parent comment read error", func(t *testing.T) {
		contentType, raw := buildMultipartBody(t, map[string][]string{
			"parent_comment_id": {"parent-id-that-is-long-enough"},
		}, nil)
		cutoff := bytes.Index(raw, []byte("parent-id-that-is-long-enough")) + 6
		req := httptest.NewRequest(http.MethodPost, "/upload", &failingReader{data: raw, failAfter: cutoff})
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected parent comment read error")
		}
	})

	t.Run("remove attachment read error", func(t *testing.T) {
		contentType, raw := buildMultipartBody(t, map[string][]string{
			"remove_attachment_ids": {"attachment-id-that-is-long-enough"},
		}, nil)
		cutoff := bytes.Index(raw, []byte("attachment-id-that-is-long-enough")) + 8
		req := httptest.NewRequest(http.MethodPost, "/upload", &failingReader{data: raw, failAfter: cutoff})
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected attachment removal read error")
		}
	})

	t.Run("next part error", func(t *testing.T) {
		contentType, raw := buildMultipartBody(t, map[string][]string{
			"body": {"Body that parses successfully"},
		}, nil)
		req := httptest.NewRequest(http.MethodPost, "/upload", &failingReader{data: raw, failAfter: len(raw) - 1})
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected next part read error")
		}
	})

	t.Run("file copy error", func(t *testing.T) {
		contentType, raw := buildMultipartBody(t, nil, []multipartFilePayload{{
			FieldName:   "files",
			Filename:    "attachment.txt",
			ContentType: "text/plain",
			Content:     []byte("file content that is long enough to fail"),
		}})
		cutoff := bytes.Index(raw, []byte("file content that is long enough to fail")) + 10
		req := httptest.NewRequest(http.MethodPost, "/upload", &failingReader{data: raw, failAfter: cutoff})
		req.Header.Set("Content-Type", contentType)
		if _, cleanup, err := readIssueCommentMultipart(httptest.NewRecorder(), req, "UI"); err == nil {
			cleanup()
			t.Fatal("expected file copy error")
		}
	})
}

func TestIssueCommentAndAssetRoutesRejectInvalidMethods(t *testing.T) {
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

	putAssets := requestJSON(t, srv, http.MethodPut, "/api/v1/app/issues/"+issue.Identifier+"/assets", nil)
	if putAssets.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("assets collection expected 405, got %d", putAssets.StatusCode)
	}
	_ = putAssets.Body.Close()

	missingAsset := requestJSON(t, srv, http.MethodGet, "/api/v1/app/issues/"+issue.Identifier+"/assets/missing/content", nil)
	if missingAsset.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset content expected 404, got %d", missingAsset.StatusCode)
	}
	_ = decodeResponse(t, missingAsset)
}
