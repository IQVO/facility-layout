package main_test

// Wave-2 step definitions: generic REST request and JSON-path assertion steps
// used by the geometry, fixed-structure, cross-aisle, travel-graph and
// distance features, plus the few domain seeds those features share. Every
// scenario still drives the real HTTP server; outcomes are asserted from the
// response and the published domain events only.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// rawRequest sends a request whose body is the verbatim text (possibly not
// valid JSON, to exercise malformed-body handling) and records the response.
func (w *world) rawRequest(ctx context.Context, method, path, body string) (int, []byte, http.Header, error) {
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, w.server.URL+path, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.server.Client().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, raw, resp.Header, nil
}

func (w *world) aRequestIsSentTo(ctx context.Context, method, path string) error {
	return w.aRequestIsSentWithBody(ctx, method, path, "")
}

func (w *world) aRequestIsSentWithBody(ctx context.Context, method, path, body string) error {
	status, raw, headers, err := w.rawRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	w.status, w.body, w.headers = status, raw, headers
	return nil
}

// aRequestHasBeenAccepted is the Given form: an out-of-band request that must
// succeed, so a broken fixture never masquerades as a failing assertion.
func (w *world) aRequestHasBeenAccepted(ctx context.Context, method, path, body string) error {
	status, raw, _, err := w.rawRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("seeding %s %s: expected 2xx, got %d: %s", method, path, status, raw)
	}
	return nil
}

func (w *world) aOneWayAisle(ctx context.Context, aisleCode, zoneID string, hint int) error {
	return w.seed(ctx, http.MethodPost, "/zones/"+zoneID+"/aisles", map[string]any{
		"aisleCode": aisleCode, "sequenceHint": hint, "direction": "OneWay",
	}, http.StatusCreated)
}

// baysHaveSlots seeds one slot per listed bay on an aisle (level 01, position A).
func (w *world) baysHaveSlots(ctx context.Context, bays, aisle, zoneID, locationType string) error {
	for _, bay := range strings.Split(bays, ",") {
		code := zoneID + "-" + aisle + "-" + strings.TrimSpace(bay) + "-01-A"
		if err := w.aRegisteredSlot(ctx, code, locationType); err != nil {
			return err
		}
	}
	return nil
}

// aZoneWithPitches registers a zone carrying the travel-graph pitch overrides.
func (w *world) aZoneWithPitches(ctx context.Context, areaCode, zoneCode, siteCode string, bay, level float64) error {
	return w.seed(ctx, http.MethodPost, "/sites/"+siteCode+"/zones", map[string]any{
		"areaCode": areaCode, "zoneCode": zoneCode, "temperatureClass": "Ambient",
		"bayPitchM": bay, "levelPitchM": level,
	}, http.StatusCreated)
}

// jsonAt walks a dotted path ("edges.0.from.bay"; "" is the root) through the
// last response body.
func (w *world) jsonAt(path string) (any, bool, error) {
	var doc any
	if err := w.decode(&doc); err != nil {
		return nil, false, err
	}
	if path == "" {
		return doc, true, nil
	}
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false, nil
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false, nil
			}
			cur = node[i]
		default:
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func (w *world) theResponseFieldIs(path, literal string) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("response has no field %q: %s", path, w.body)
	}
	var want any
	if err := json.Unmarshal([]byte(literal), &want); err != nil {
		return fmt.Errorf("expected value %q is not a JSON literal: %w", literal, err)
	}
	if gf, isNum := got.(float64); isNum {
		wf, wantNum := want.(float64)
		if !wantNum || math.Abs(gf-wf) > 1e-9 {
			return fmt.Errorf("field %q: expected %s, got %v", path, literal, got)
		}
		return nil
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("field %q: expected %s, got %v", path, literal, got)
	}
	return nil
}

func (w *world) theResponseHasNoField(path string) error {
	_, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("expected no field %q in %s", path, w.body)
	}
	return nil
}

func (w *world) theResponseListHasItems(path string, n int) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	list, isList := got.([]any)
	if !ok || !isList || len(list) != n {
		return fmt.Errorf("expected %q to be a list of %d items, got %s", path, n, w.body)
	}
	return nil
}

// theResponseListFieldIsOrdered asserts the values of one field across a list,
// in order, as a comma-separated string.
func (w *world) theResponseListFieldIsOrdered(field, path, want string) error {
	got, ok, err := w.jsonAt(path)
	if err != nil {
		return err
	}
	list, isList := got.([]any)
	if !ok || !isList {
		return fmt.Errorf("%q is not a list: %s", path, w.body)
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		obj, _ := item.(map[string]any)
		values = append(values, fmt.Sprint(obj[field]))
	}
	if strings.Join(values, ",") != want {
		return fmt.Errorf("expected %s values [%s], got [%s]", field, want, strings.Join(values, ","))
	}
	return nil
}

// theTravelEdgeIs finds the directed edge between two aisle/bay waypoints (the
// graph lists edges in no defined order) and checks its length and provenance.
func (w *world) theTravelEdgeIs(fromAisle, fromBay, toAisle, toBay string, metres float64, kind string) error {
	var graph struct {
		Edges []struct {
			From struct {
				AisleID string `json:"aisleId"`
				Bay     string `json:"bay"`
			} `json:"from"`
			To struct {
				AisleID string `json:"aisleId"`
				Bay     string `json:"bay"`
			} `json:"to"`
			MetresM   float64 `json:"metresM"`
			Estimated bool    `json:"estimated"`
		} `json:"edges"`
	}
	if err := w.decode(&graph); err != nil {
		return err
	}
	for _, e := range graph.Edges {
		if strings.HasSuffix(e.From.AisleID, "-"+fromAisle) && e.From.Bay == fromBay &&
			strings.HasSuffix(e.To.AisleID, "-"+toAisle) && e.To.Bay == toBay {
			if math.Abs(e.MetresM-metres) > 1e-9 || e.Estimated != (kind == "estimated") {
				return fmt.Errorf("edge %s/%s -> %s/%s: expected %v m %s, got %v m estimated=%v",
					fromAisle, fromBay, toAisle, toBay, metres, kind, e.MetresM, e.Estimated)
			}
			return nil
		}
	}
	return fmt.Errorf("no edge %s/%s -> %s/%s in %s", fromAisle, fromBay, toAisle, toBay, w.body)
}

func (w *world) exactlyNEventsPublished(n int, name string) error {
	count := 0
	for _, e := range w.publisher.Events() {
		if e.EventName() == name {
			count++
		}
	}
	if count != n {
		return fmt.Errorf("expected %d %q events, got %d", n, name, count)
	}
	return nil
}

func (w *world) registerWave2Steps(sc *godog.ScenarioContext) {
	sc.Step(`^a (GET|POST|PUT) request is sent to "([^"]*)"$`, w.aRequestIsSentTo)
	sc.Step(`^a (POST|PUT) request is sent to "([^"]*)" with the JSON body (.*)$`, w.aRequestIsSentWithBody)
	sc.Step(`^a (POST|PUT) to "([^"]*)" with the JSON body (.*) has been accepted$`, w.aRequestHasBeenAccepted)
	sc.Step(`^a registered one-way Aisle "([^"]*)" in Zone "([^"]*)" with sequence hint (\d+)$`, w.aOneWayAisle)
	sc.Step(`^bays "([^"]*)" of Aisle "([^"]*)" in Zone "([^"]*)" each have a LocationSlot of type "([^"]*)"$`, w.baysHaveSlots)
	sc.Step(`^a registered Zone "([^"]*)"/"([^"]*)" in Site "([^"]*)" with bay pitch ([\d.]+) m and level pitch ([\d.]+) m$`, w.aZoneWithPitches)
	sc.Step(`^the response field "([^"]*)" is (.+)$`, w.theResponseFieldIs)
	sc.Step(`^the response has no field "([^"]*)"$`, w.theResponseHasNoField)
	sc.Step(`^the response list "([^"]*)" has (\d+) items?$`, w.theResponseListHasItems)
	sc.Step(`^the "([^"]*)" of each item in "([^"]*)" are "([^"]*)"$`, w.theResponseListFieldIsOrdered)
	sc.Step(`^the travel edge from aisle "([^"]*)" bay "([^"]*)" to aisle "([^"]*)" bay "([^"]*)" is ([\d.]+) m and (estimated|measured)$`, w.theTravelEdgeIs)
	sc.Step(`^exactly (\d+) "([^"]*)" domain events? (?:is|are) published$`, w.exactlyNEventsPublished)
}
