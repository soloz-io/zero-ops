// Package creditauthority_test asserts the Credit Authority's guarantees
// (ADR-109 §3) against a deployed tenant ledger, through its gateway, as the
// coordinating service calls it.
//
// SKIPPED unless pointed at one:
//
//	CREDITS_URL     the Credit Authority's address (its sidecar), e.g. a port-forward
//	CREDITS_LEDGER  the tenant's ledger name
//	CREDITS_TOKEN   an access token of the tenant's coordinating service
//
// Every account it touches is named for the run, so runs never share a balance.
package creditauthority_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

const asset = "CREDIT"

type authority struct {
	url, ledger, token string
	run                string
}

func setup(t *testing.T) *authority {
	t.Helper()
	a := &authority{
		url:    os.Getenv("CREDITS_URL"),
		ledger: os.Getenv("CREDITS_LEDGER"),
		token:  os.Getenv("CREDITS_TOKEN"),
		run:    fmt.Sprintf("conformance:%d", time.Now().UnixNano()),
	}
	if a.url == "" || a.ledger == "" || a.token == "" {
		t.Skip("CREDITS_URL, CREDITS_LEDGER and CREDITS_TOKEN name the Credit Authority to test")
	}
	return a
}

func (a *authority) account(name string) string { return a.run + ":" + name }

type posting struct {
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Amount      *big.Int `json:"amount"`
	Asset       string   `json:"asset"`
}

func move(src, dst string, amount int64) posting {
	return posting{Source: src, Destination: dst, Amount: big.NewInt(amount), Asset: asset}
}

type result struct {
	status int
	id     *big.Int
	code   string
}

// post creates a transaction from a raw body, with an optional idempotency key
// and query, and reports what the Credit Authority answered.
func (a *authority) post(t *testing.T, body any, key, query string) result {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v2/%s/transactions%s", a.url, a.ledger, query), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("authorization", "Bearer "+a.token)
	req.Header.Set("content-type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Data struct {
			ID *big.Int `json:"id"`
		} `json:"data"`
		ErrorCode string `json:"errorCode"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return result{status: res.StatusCode, id: out.Data.ID, code: out.ErrorCode}
}

func (a *authority) transfer(t *testing.T, key string, postings ...posting) result {
	t.Helper()
	return a.post(t, map[string]any{"postings": postings}, key, "")
}

// balance reads an account's balance in the test asset.
func (a *authority) balance(t *testing.T, account string) *big.Int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v2/%s/accounts/%s?expand=volumes", a.url, a.ledger, account), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("authorization", "Bearer "+a.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("read %s: HTTP %d", account, res.StatusCode)
	}
	var out struct {
		Data struct {
			Volumes map[string]struct {
				Balance *big.Int `json:"balance"`
			} `json:"volumes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if v, ok := out.Data.Volumes[asset]; ok && v.Balance != nil {
		return v.Balance
	}
	return big.NewInt(0)
}

func (a *authority) fund(t *testing.T, account string, amount int64) {
	t.Helper()
	if r := a.transfer(t, "", move("world", account, amount)); r.status != http.StatusOK {
		t.Fatalf("fund %s: HTTP %d %s", account, r.status, r.code)
	}
}

func eq(t *testing.T, what string, got *big.Int, want int64) {
	t.Helper()
	if got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %s, want %d", what, got, want)
	}
}

// Atomicity: a transaction one of whose postings cannot be covered commits
// none of them.
func TestATransactionCommitsWholeOrNotAtAll(t *testing.T) {
	a := setup(t)
	src, ok, short := a.account("src"), a.account("ok"), a.account("short")
	a.fund(t, src, 10)

	r := a.transfer(t, "", move(src, ok, 5), move(short, ok, 1))
	if r.status != http.StatusBadRequest || r.code != "INSUFFICIENT_FUND" {
		t.Fatalf("got HTTP %d %s, want 400 INSUFFICIENT_FUND", r.status, r.code)
	}
	eq(t, "the covered posting's source", a.balance(t, src), 10)
	eq(t, "the covered posting's destination", a.balance(t, ok), 0)
}

// No overdraft, and serialisable balances: concurrent spends of one balance
// commit exactly as many as it covers, and it never goes below zero.
func TestConcurrentSpendsNeverExceedTheBalance(t *testing.T) {
	a := setup(t)
	pool, sink := a.account("pool"), a.account("sink")
	const funded, each, attempts = 100, 10, 30
	a.fund(t, pool, funded)

	var mu sync.Mutex
	committed, refused := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a.transfer(t, "", move(pool, sink, each))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case r.status == http.StatusOK:
				committed++
			case r.code == "INSUFFICIENT_FUND":
				refused++
			default:
				t.Errorf("unexpected answer: HTTP %d %s", r.status, r.code)
			}
		}()
	}
	wg.Wait()

	if committed != funded/each || refused != attempts-funded/each {
		t.Fatalf("committed %d and refused %d; want exactly %d and %d", committed, refused, funded/each, attempts-funded/each)
	}
	eq(t, "the pool", a.balance(t, pool), 0)
	eq(t, "the sink", a.balance(t, sink), funded)
}

// Idempotency: a repeat with the same key returns the original transaction
// and moves nothing again; the same key with different input is refused.
func TestARepeatedKeyReturnsTheOriginalAndMovesNothing(t *testing.T) {
	a := setup(t)
	src, dst := a.account("src"), a.account("dst")
	a.fund(t, src, 10)
	key := a.run + ":once"

	first := a.transfer(t, key, move(src, dst, 4))
	again := a.transfer(t, key, move(src, dst, 4))
	if first.status != http.StatusOK || again.status != http.StatusOK || first.id.Cmp(again.id) != 0 {
		t.Fatalf("first %d id %v, repeat %d id %v; want the same transaction twice", first.status, first.id, again.status, again.id)
	}
	eq(t, "the destination", a.balance(t, dst), 4)

	other := a.transfer(t, key, move(src, dst, 5))
	if other.status == http.StatusOK {
		t.Fatal("the same key with different input committed")
	}
	eq(t, "the destination after a conflicting reuse", a.balance(t, dst), 4)
}

// A failed transaction stores no key, so the same operation can be retried.
func TestAFailedTransactionCanBeRetriedUnderItsKey(t *testing.T) {
	a := setup(t)
	src, dst := a.account("src"), a.account("dst")
	key := a.run + ":retry"

	if r := a.transfer(t, key, move(src, dst, 3)); r.code != "INSUFFICIENT_FUND" {
		t.Fatalf("got HTTP %d %s, want INSUFFICIENT_FUND", r.status, r.code)
	}
	a.fund(t, src, 3)
	if r := a.transfer(t, key, move(src, dst, 3)); r.status != http.StatusOK {
		t.Fatalf("retry under the same key: HTTP %d %s", r.status, r.code)
	}
	eq(t, "the destination", a.balance(t, dst), 3)
}

// Traceability: a transaction keeps the metadata it was created with.
func TestATransactionKeepsItsMetadata(t *testing.T) {
	a := setup(t)
	src, dst := a.account("src"), a.account("dst")
	a.fund(t, src, 1)
	r := a.post(t, map[string]any{
		"postings": []posting{move(src, dst, 1)},
		"metadata": map[string]string{"operation": a.run},
	}, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("HTTP %d %s", r.status, r.code)
	}

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v2/%s/transactions/%s", a.url, a.ledger, r.id), nil)
	req.Header.Set("authorization", "Bearer "+a.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Data struct {
			Metadata map[string]string `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Metadata["operation"] != a.run {
		t.Fatalf("metadata = %v, want operation=%s", out.Data.Metadata, a.run)
	}
}

// No opt-out of the balance check reaches the ledger (ADR-109 §4): each of
// these would otherwise overdraw an unfunded account.
func TestAnOptOutOfTheBalanceCheckIsRefused(t *testing.T) {
	a := setup(t)
	empty, dst := a.account("empty"), a.account("dst")
	postings := []posting{move(empty, dst, 1)}
	script := fmt.Sprintf("send [%s 1] (\n source = @%s allowing unbounded overdraft\n destination = @%s\n)", asset, empty, dst)

	for name, tc := range map[string]struct {
		body  any
		query string
	}{
		"force in the query":     {body: map[string]any{"postings": postings}, query: "?force=true"},
		"force in the body":      {body: map[string]any{"postings": postings, "force": true}},
		"Force in another case":  {body: map[string]any{"postings": postings, "Force": true}},
		"a Numscript overdraft":  {body: map[string]any{"script": map[string]string{"plain": script}}},
		"a back-dated timestamp": {body: map[string]any{"postings": postings, "timestamp": "2000-01-01T00:00:00Z"}},
	} {
		t.Run(name, func(t *testing.T) {
			if r := a.post(t, tc.body, "", tc.query); r.status == http.StatusOK {
				t.Fatal("committed")
			}
		})
	}
	eq(t, "the unfunded account", a.balance(t, empty), 0)
}
