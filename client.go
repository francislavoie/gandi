package gandi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/libdns/libdns"
)

func (p *Provider) setRecord(ctx context.Context, domain gandiDomain, record libdns.Record) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s/%s", domain.ZoneRecordsHref, record.Name, record.Type), nil)
	if err != nil {
		return err
	}

	var oldGandiRecord gandiRecord
	status, err := p.doRequest(req, &oldGandiRecord)

	// ignore if no record is found, then we can create one safely
	if status.Code != http.StatusNotFound && err != nil {
		return err
	}

	// we check if the new value does not already exists and append it if not
	var exists bool
	for _, val := range oldGandiRecord.RRSetValues {
		if val == record.Value {
			exists = true
			break
		}
	}

	values := oldGandiRecord.RRSetValues
	if !exists {
		values = append(values, record.Value)
	}

	// we just create a new record, if an existing record was found, we just append the new value to the existing ones
	newGandiRecord := gandiRecord{
		RRSetType:   record.Type,
		RRSetName:   record.Name,
		RRSetTTL:    int(record.TTL.Seconds()),
		RRSetValues: values,
	}

	raw, err := json.Marshal(newGandiRecord)
	if err != nil {
		return err
	}

	// we update existing record or create a new record if it does not exist yet
	req, err = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/%s/%s", domain.ZoneRecordsHref, record.Name, record.Type), bytes.NewReader(raw))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	_, err = p.doRequest(req, nil)
	return err
}

func (p *Provider) deleteRecord(ctx context.Context, domain gandiDomain, record libdns.Record) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s/%s", domain.ZoneRecordsHref, record.Name, record.Type), nil)
	if err != nil {
		return err
	}

	// check if the record exists beforehand
	var rec gandiRecord
	_, err = p.doRequest(req, &rec)
	if err != nil {
		return err
	}

	if len(rec.RRSetValues) > 1 {
		// if it contains multiple values, the best is to update the record instead of deleting all the values
		for i, val := range rec.RRSetValues {
			if val == record.Value {
				rec.RRSetValues[len(rec.RRSetValues)-1], rec.RRSetValues[i] = rec.RRSetValues[i], rec.RRSetValues[len(rec.RRSetValues)-1]
				rec.RRSetValues = rec.RRSetValues[:len(rec.RRSetValues)-1]
			}
		}

		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}

		req, err = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/%s/%s", domain.ZoneRecordsHref, record.Name, record.Type), bytes.NewReader(raw))
	} else {
		// if there is only one entry, we make sure that the value to delete is matching the one we found
		// otherwise we may delete the wrong record
		if rec.RRSetValues[0] != record.Value {
			return fmt.Errorf("LiveDNS returned a %v (%v)", http.StatusNotFound, "Can't find such a DNS value")
		}

		req, err = http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/%s/%s", domain.ZoneRecordsHref, record.Name, record.Type), nil)
	}

	// we check if NewRequestWithContext threw an error
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	_, err = p.doRequest(req, nil)
	return err
}

func (p *Provider) getDomain(ctx context.Context, fqdn string) (gandiDomain, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.domains == nil {
		p.domains = make(map[string]gandiDomain)
	}
	if domain, ok := p.domains[fqdn]; ok {
		return domain, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://dns.api.gandi.net/api/v5/domains/%s", fqdn), nil)

	var domain gandiDomain

	if _, err = p.doRequest(req, &domain); err != nil {
		return gandiDomain{}, err
	}

	p.domains[fqdn] = domain

	return domain, nil
}

func (p *Provider) doRequest(req *http.Request, result interface{}) (gandiStatus, error) {
	req.Header.Set("X-Api-Key", p.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return gandiStatus{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var response gandiStatus
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return gandiStatus{}, err
		}

		return response, fmt.Errorf("LiveDNS returned a %v (%v)", response.Code, response.Message)
	}

	// the api does not return the json object on 201 or 204, so we just stop here
	if resp.StatusCode > 200 {
		return gandiStatus{}, nil
	}

	// if we get a 200, we parse the json object
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return gandiStatus{}, err
	}

	return gandiStatus{}, nil
}
