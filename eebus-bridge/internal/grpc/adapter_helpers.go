package grpc

import (
	"context"
	"log"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type streamSender[T any] func(*T) error
type eventConverter[T any] func(eebus.Event) (*T, bool)
type revisionedInitialEvent[T any] func(ski string, revision uint64, eventTime time.Time) *T
type compatibleEntityResolver func(string) eebus.EntityResolution

func requireDeviceRequest(req *pb.DeviceRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	return nil
}

func normalizeReadSKI(ski string) (string, error) {
	normalized := eebus.NormalizeSKI(ski)
	if normalized != "" && !validSKI(normalized) {
		return "", status.Error(codes.InvalidArgument, "ski must be 40 hex characters")
	}
	return normalized, nil
}

func requireExplicitSKI(ski string) (string, error) {
	normalized := eebus.NormalizeSKI(ski)
	if normalized == "" {
		return "", status.Error(codes.InvalidArgument, "ski is required")
	}
	if !validSKI(normalized) {
		return "", status.Error(codes.InvalidArgument, "ski must be 40 hex characters")
	}
	return normalized, nil
}

func requireWriteSKI(ski string) error {
	normalized := eebus.NormalizeSKI(ski)
	if normalized == "" {
		return status.Error(codes.InvalidArgument, "ski is required for write operations")
	}
	if !validSKI(normalized) {
		return status.Error(codes.InvalidArgument, "ski must be 40 hex characters")
	}
	return nil
}

func subscribeFilteredEvents[T any](
	bus *eebus.EventBus,
	req *pb.DeviceRequest,
	ctx context.Context,
	send streamSender[T],
	convert eventConverter[T],
) error {
	if err := requireDeviceRequest(req); err != nil {
		return err
	}
	reqSKI, err := normalizeReadSKI(req.Ski)
	if err != nil {
		return err
	}
	return subscribeEvents(bus, reqSKI, ctx, send, convert)
}

func subscribeAllEvents[T any](
	bus *eebus.EventBus,
	ctx context.Context,
	send streamSender[T],
	convert eventConverter[T],
) error {
	return subscribeEvents(bus, "", ctx, send, convert)
}

func subscribeEvents[T any](
	bus *eebus.EventBus,
	reqSKI string,
	ctx context.Context,
	send streamSender[T],
	convert eventConverter[T],
) error {
	if bus == nil {
		return status.Error(codes.Unavailable, "event bus not initialized")
	}

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			if reqSKI != "" && eebus.NormalizeSKI(evt.SKI) != reqSKI {
				continue
			}
			event, ok := convert(evt)
			if !ok {
				continue
			}
			if err := send(event); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func subscribeRevisionedEvents[T any](
	bus *eebus.EventBus,
	req *pb.DeviceRequest,
	ctx context.Context,
	send streamSender[T],
	initial revisionedInitialEvent[T],
	convert eventConverter[T],
) error {
	if err := requireDeviceRequest(req); err != nil {
		return err
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return err
	}
	if bus == nil {
		return status.Error(codes.Unavailable, "event bus not initialized")
	}

	ch, revision := bus.SubscribeWithRevision(ski)
	defer bus.Unsubscribe(ch)
	if err := send(initial(ski, revision, time.Now().UTC())); err != nil {
		return err
	}

	for {
		if evt, pending := bus.TakePendingResync(ch); pending {
			event, ok := convert(evt)
			if !ok {
				continue
			}
			if err := send(event); err != nil {
				return err
			}
			continue
		}
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			event, ok := convert(evt)
			if !ok {
				continue
			}
			if err := send(event); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func debugLogf(enabled bool, format string, args ...any) {
	if enabled {
		log.Printf(format, args...)
	}
}

func redactedSKIForLog(ski string) string {
	normalized := eebus.NormalizeSKI(ski)
	if normalized == "" {
		return "empty"
	}
	if !validSKI(normalized) {
		return "invalid"
	}
	return eebus.ShortSKI(normalized)
}

func requestedSKIForError(ski string) string {
	if eebus.NormalizeSKI(ski) == "" {
		return "empty"
	}
	return "specified"
}

func resolveCompatibleEntity(
	ski string,
	entityLabel string,
	capability eebus.Capability,
	registry *eebus.DeviceRegistry,
	resolve compatibleEntityResolver,
) (spineapi.EntityRemoteInterface, error) {
	resolution, err := resolveCompatibleEntityResolution(ski, entityLabel, capability, registry, resolve)
	if err != nil {
		return nil, err
	}
	return resolution.Entity, nil
}

func resolveCompatibleEntityResolution(
	ski string,
	entityLabel string,
	capability eebus.Capability,
	registry *eebus.DeviceRegistry,
	resolve compatibleEntityResolver,
) (eebus.EntityResolution, error) {
	if _, err := normalizeReadSKI(ski); err != nil {
		return eebus.EntityResolution{}, err
	}
	if resolve != nil {
		resolution := resolve(ski)
		if resolution.Ambiguous() {
			return eebus.EntityResolution{}, ambiguousDeviceSelection(resolution.DeviceCount)
		}
		if resolution.Entity != nil {
			return resolution, nil
		}
		if registry != nil {
			registry.RecordCapabilityMissingEntity(ski, capability)
		}
		return eebus.EntityResolution{}, status.Errorf(
			codes.NotFound,
			"no compatible %s found for %s ski",
			entityLabel,
			requestedSKIForError(ski),
		)
	}
	if registry == nil {
		return eebus.EntityResolution{}, status.Error(codes.Unavailable, "device registry not initialized")
	}
	registry.RecordCapabilityMissingEntity(ski, capability)
	return eebus.EntityResolution{}, status.Errorf(
		codes.NotFound,
		"no compatible %s found for %s ski",
		entityLabel,
		requestedSKIForError(ski),
	)
}
