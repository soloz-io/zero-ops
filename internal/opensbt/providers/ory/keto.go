package ory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type ketoClient struct {
	readURL  string
	writeURL string
	client   *http.Client
}

type ketoRelationship struct {
	Namespace string `json:"namespace"`
	Object    string `json:"object"`
	Relation  string `json:"relation"`
	SubjectID string `json:"subject_id"`
}

// createRelationship creates a Keto relationship tuple.
// e.g. namespace=tenants, object=tenant-123, relation=member, subject=user-456
func (k *ketoClient) createRelationship(ctx context.Context, rel ketoRelationship) error {
	body, _ := json.Marshal(rel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		k.writeURL+"/admin/relation-tuples", nil)
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytesReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("keto createRelationship: %d %s", resp.StatusCode, b)
	}
	return nil
}

// deleteRelationship removes a Keto relationship tuple.
func (k *ketoClient) deleteRelationship(ctx context.Context, rel ketoRelationship) error {
	q := url.Values{
		"namespace":  {rel.Namespace},
		"object":     {rel.Object},
		"relation":   {rel.Relation},
		"subject_id": {rel.SubjectID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		k.writeURL+"/admin/relation-tuples?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("keto deleteRelationship: %d %s", resp.StatusCode, b)
	}
	return nil
}

// check returns true if the subject has the given relation on the object.
func (k *ketoClient) check(ctx context.Context, rel ketoRelationship) (bool, error) {
	q := url.Values{
		"namespace":  {rel.Namespace},
		"object":     {rel.Object},
		"relation":   {rel.Relation},
		"subject_id": {rel.SubjectID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		k.readURL+"/relation-tuples/check?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	return result.Allowed, nil
}

func bytesReader(b []byte) io.Reader {
	return &bytesReaderImpl{data: b}
}

type bytesReaderImpl struct {
	data []byte
	pos  int
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
