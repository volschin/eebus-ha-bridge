package usecases

import "sync"

// providerAvailability mirrors a provider's data state into the advertised
// `useCaseAvailable` flag of nodeManagementUseCaseData.
//
// The bridge's provider use cases (MGCP, VAPD, VABD) serve data that originates in
// Home Assistant. Announcing them permanently as available is wrong whenever that
// source has not delivered yet, has expired, or the provider was closed: a consumer
// would bind and read empty measurements instead of skipping the use case.
//
// eebus-go documents UpdateUseCaseAvailability as client-side only and points
// server-side implementations at RemoveUseCase/AddUseCase instead. The bridge
// deliberately uses the availability flag anyway: removing and re-adding a use case
// re-announces the whole use-case map and drops the consumer's bindings and
// subscriptions, which would churn on every sample gap. Flipping availability is
// exactly what the flag is specified for and leaves the features in place.
//
// Transitions are deduplicated, so a provider publishing every few seconds emits a
// single notification when its state actually changes.
type providerAvailability struct {
	mu        sync.Mutex
	update    func(bool)
	known     bool
	available bool
}

// bind installs the use case's availability setter. Providers call this once their
// embedded UseCaseBase exists.
func (a *providerAvailability) bind(update func(bool)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.update = update
}

// set announces available when it differs from the last announced value. Safe
// before bind: the state is remembered but nothing is announced.
func (a *providerAvailability) set(available bool) {
	a.mu.Lock()
	if a.known && a.available == available {
		a.mu.Unlock()
		return
	}
	a.known = true
	a.available = available
	update := a.update
	a.mu.Unlock()

	if update != nil {
		update(available)
	}
}
