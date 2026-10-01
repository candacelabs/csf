package email

import (
	"errors"
	"fmt"
	"strings"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

const (
	maxSessions       = 16
	maxContainers     = 32
	maxEvidenceLinks  = 64
	maxContainerLinks = 16
	maxUnavailable    = 32
	maxUnavailableLen = 256
)

func validateReceipt(receipt *emailv1.EmailReceipt) error {
	if err := emailv1.ValidateEmailReceipt(receipt); err != nil {
		return err
	}
	if receipt.Provenance == nil {
		return errors.New("email: receipt provenance is required")
	}
	if receipt.CompletedAt != nil {
		if err := receipt.CompletedAt.CheckValid(); err != nil {
			return fmt.Errorf("email: invalid completion time: %w", err)
		}
	}
	return validateMetadata(receipt.Provenance)
}

func validateMetadata(metadata *provenancev1.ReceiptMetadata) error {
	if err := provenancev1.ValidateReceiptMetadata(metadata); err != nil {
		return err
	}
	if metadata.RecordedAt == nil {
		return errors.New("email: provenance recorded_at is required")
	}
	if err := metadata.RecordedAt.CheckValid(); err != nil {
		return fmt.Errorf("email: invalid provenance recorded_at: %w", err)
	}
	if metadata.ReportingNode != nil {
		if err := provenancev1.ValidateNodeIdentity(metadata.ReportingNode); err != nil {
			return err
		}
	}
	if len(metadata.Sessions) > maxSessions {
		return fmt.Errorf("email: provenance sessions exceed %d", maxSessions)
	}
	if len(metadata.Containers) > maxContainers {
		return fmt.Errorf("email: provenance containers exceed %d", maxContainers)
	}
	if len(metadata.Links) > maxEvidenceLinks {
		return fmt.Errorf("email: provenance links exceed %d", maxEvidenceLinks)
	}
	if len(metadata.Unavailable) > maxUnavailable {
		return fmt.Errorf("email: provenance unavailable entries exceed %d", maxUnavailable)
	}
	for _, session := range metadata.Sessions {
		if err := validateSession(session); err != nil {
			return err
		}
	}
	for _, container := range metadata.Containers {
		if err := validateContainer(container); err != nil {
			return err
		}
	}
	for _, link := range metadata.Links {
		if err := validateLink(link); err != nil {
			return err
		}
	}
	for _, unavailable := range metadata.Unavailable {
		if strings.TrimSpace(unavailable) == "" || len(unavailable) > maxUnavailableLen {
			return errors.New("email: invalid provenance unavailable entry")
		}
	}
	return nil
}

func validateMetadataShape(metadata *provenancev1.ReceiptMetadata) error {
	if metadata == nil {
		return errors.New("email: provenance metadata is nil")
	}
	if len(metadata.Sessions) > maxSessions || len(metadata.Containers) > maxContainers ||
		len(metadata.Links) > maxEvidenceLinks || len(metadata.Unavailable) > maxUnavailable {
		return errors.New("email: provenance repeated fields exceed bounds")
	}
	for _, container := range metadata.Containers {
		if container == nil || len(container.Links) > maxContainerLinks {
			return errors.New("email: container evidence exceeds bounds")
		}
	}
	return nil
}

func sanitizeMetadataURLs(metadata *provenancev1.ReceiptMetadata) {
	omitted := false
	for _, session := range metadata.Sessions {
		if session != nil && session.Url != "" && safeURL(session.Url) == "" {
			session.Url = ""
			omitted = true
		}
	}
	metadata.Links, omitted = sanitizedLinks(metadata.Links, omitted)
	for _, container := range metadata.Containers {
		container.Links, omitted = sanitizedLinks(container.Links, omitted)
	}
	if omitted && len(metadata.Unavailable) < maxUnavailable {
		metadata.Unavailable = append(metadata.Unavailable, unsafeURLUnavailable)
	}
}

func sanitizedLinks(links []*provenancev1.EvidenceLink, omitted bool) ([]*provenancev1.EvidenceLink, bool) {
	safe := make([]*provenancev1.EvidenceLink, 0, len(links))
	for _, link := range links {
		if link == nil || safeURL(link.GetUrl()) == "" {
			omitted = true
			continue
		}
		safe = append(safe, link)
	}
	return safe, omitted
}

func validateSession(session *provenancev1.SessionReference) error {
	if err := provenancev1.ValidateSessionReference(session); err != nil {
		return err
	}
	if session.GetUrl() != "" && safeURL(session.GetUrl()) == "" {
		return errors.New("email: unsafe session URL")
	}
	return nil
}

func validateContainer(container *provenancev1.ContainerObservation) error {
	if err := provenancev1.ValidateContainerObservation(container); err != nil {
		return err
	}
	if container.Node != nil {
		if err := provenancev1.ValidateNodeIdentity(container.Node); err != nil {
			return err
		}
	}
	if container.ObservedAt != nil {
		if err := container.ObservedAt.CheckValid(); err != nil {
			return fmt.Errorf("email: invalid container observed_at: %w", err)
		}
	}
	if len(container.Links) > maxContainerLinks {
		return fmt.Errorf("email: container links exceed %d", maxContainerLinks)
	}
	for _, link := range container.Links {
		if err := validateLink(link); err != nil {
			return err
		}
	}
	return nil
}

func validateLink(link *provenancev1.EvidenceLink) error {
	if err := provenancev1.ValidateEvidenceLink(link); err != nil {
		return err
	}
	if safeURL(link.GetUrl()) == "" {
		return errors.New("email: unsafe evidence URL")
	}
	return nil
}
