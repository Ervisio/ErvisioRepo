package v1gen

import (
	"bytes"
	"encoding/json"
	"errors"
)

type remotePresenceUpdate struct {
	Active bool `json:"active"`
}

func (r *remotePresenceUpdate) UnmarshalJSON(data []byte) error {
	var body struct {
		Active *bool `json:"active"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Active == nil {
		return errors.New("expected an object with an active boolean")
	}
	r.Active = *body.Active
	return nil
}
