package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"encoding/json"

	strfmt "github.com/go-openapi/strfmt"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/validate"
)

// ErrorType Error Types
// swagger:model ErrorType
type ErrorType string

const (
	ErrorTypeUnknownError           ErrorType = "UnknownError"
	ErrorTypeInvalidDomain          ErrorType = "InvalidDomain"
	ErrorTypeUnkownVersion          ErrorType = "UnkownVersion"
	ErrorTypeInvalidRecord          ErrorType = "InvalidRecord"
	ErrorTypeInvalidRequest         ErrorType = "InvalidRequest"
	ErrorTypeInvalidResponse        ErrorType = "InvalidResponse"
	ErrorTypeInvalidProtobufMessage ErrorType = "InvalidProtobufMessage"
	ErrorTypeInvalidJSON            ErrorType = "InvalidJSON"
	ErrorTypeFailedToOpenEnvelope   ErrorType = "FailedToOpenEnvelope"
	ErrorTypeInvalidStateTransition ErrorType = "InvalidStateTransition"
	ErrorTypeUnauthorized           ErrorType = "Unauthorized"
	ErrorTypeResourceConflict       ErrorType = "ResourceConflict"
	ErrorTypeResourceExist          ErrorType = "ResourceExist"
	ErrorTypeResourceNotFound       ErrorType = "ResourceNotFound"
	ErrorTypeRouterError            ErrorType = "RouterError"
	ErrorTypeSoftLayerAPIError      ErrorType = "SoftLayerAPIError"
	ErrorTypeGUIDGeneration         ErrorType = "GUIDGeneration"
	ErrorTypeDeserialize            ErrorType = "Deserialize"
	ErrorTypeDeadlock               ErrorType = "Deadlock"
	ErrorTypeUnrecoverable          ErrorType = "Unrecoverable"
)

// for schema
var errorTypeEnum []interface{}

func init() {
	var res []ErrorType
	if err := json.Unmarshal([]byte(`["UnknownError","InvalidDomain","UnkownVersion","InvalidRecord","InvalidRequest","InvalidResponse","InvalidProtobufMessage","InvalidJSON","FailedToOpenEnvelope","InvalidStateTransition","Unauthorized","ResourceConflict","ResourceExist","ResourceNotFound","RouterError","SoftLayerAPIError","GUIDGeneration","Deserialize","Deadlock","Unrecoverable"]`), &res); err != nil {
		panic(err)
	}
	for _, v := range res {
		errorTypeEnum = append(errorTypeEnum, v)
	}
}

func (m ErrorType) validateErrorTypeEnum(path, location string, value ErrorType) error {
	if err := validate.Enum(path, location, value, errorTypeEnum); err != nil {
		return err
	}
	return nil
}

// Validate validates this error type
func (m ErrorType) Validate(formats strfmt.Registry) error {
	var res []error

	// value enum
	if err := m.validateErrorTypeEnum("", "body", m); err != nil {
		return err
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}
