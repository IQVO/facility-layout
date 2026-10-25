// Package shared holds the value objects, domain events, and error types
// common to every aggregate in the Facility Layout domain: the coded
// LocationCode, the Capacity envelope, the lifecycle Status shared by every
// structural aggregate, and the eight past-tense domain events this
// bounded context publishes as its Published Language.
package shared

import "errors"

var (
	// ErrMalformedLocationCode is returned when a location code string does
	// not consist of exactly seven hyphen-joined segments.
	ErrMalformedLocationCode = errors.New("location code must have exactly 7 hyphen-joined segments (site-area-zone-aisle-bay-level-position)")
	// ErrEmptyLocationSegment is returned when any of the seven segments is empty.
	ErrEmptyLocationSegment = errors.New("location code segment must not be empty")
	// ErrInvalidLocationSegment is returned when a segment contains a
	// character outside [A-Z0-9].
	ErrInvalidLocationSegment = errors.New("location code segment must contain only uppercase letters and digits")

	// ErrInvalidMaxWeight is returned when a capacity envelope's maximum
	// weight is not strictly positive.
	ErrInvalidMaxWeight = errors.New("capacity max weight must be greater than zero")
	// ErrInvalidMaxVolume is returned when a capacity envelope's maximum
	// volume is not strictly positive.
	ErrInvalidMaxVolume = errors.New("capacity max volume must be greater than zero")

	// ErrUnknownTemperatureClass is returned when a temperature class is not
	// one of Ambient/Chilled/Frozen.
	ErrUnknownTemperatureClass = errors.New("temperature class must be one of Ambient, Chilled, Frozen")
	// ErrUnknownDirection is returned when an aisle direction is not one of
	// OneWay/TwoWay.
	ErrUnknownDirection = errors.New("aisle direction must be one of OneWay, TwoWay")
	// ErrUnknownStatus is returned when a lifecycle status is not one of
	// Active/UnderMaintenance/Decommissioned.
	ErrUnknownStatus = errors.New("status must be one of Active, UnderMaintenance, Decommissioned")
	// ErrUnknownHandlingUnit is returned when a handling unit is not one of
	// EACH/CASE/PALLET (ADR-0040).
	ErrUnknownHandlingUnit = errors.New("handling unit must be one of EACH, CASE, PALLET")
)
