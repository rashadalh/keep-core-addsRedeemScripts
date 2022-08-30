package generator

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/keep-network/keep-core/pkg/internal/testutils"
)

// TestGetNow covers the most basic path - calling `GetNow()` function multiple
// times and making sure result is always returned, assuming there are no errors
// from the persistence layer.
func TestGetNow(t *testing.T) {
	pool, scheduler, _ := newTestPool(5)
	defer scheduler.stop()

	for {
		if pool.CurrentSize() == 5 {
			break
		}
	}

	for i := 0; i < 5; i++ {
		e, err := pool.GetNow()
		if err != nil {
			t.Errorf("unexpected error: [%v]", err)
		}
		if e == nil {
			t.Errorf("expected not-nil parameter")
		}
	}
}

// TestGetNow_EmptyPool covers the basic unhappy path when the parameter
// pool is empty and the `GetNow` function should break the execution
// and return an appropriate error.
func TestGetNow_EmptyPool(t *testing.T) {
	pool, scheduler, _ := newTestPool(
		5,
		func(ctx context.Context) *big.Int {
			<-ctx.Done()
			return nil
		},
	)
	defer scheduler.stop()

	_, err := pool.GetNow()

	expectedErr := fmt.Errorf("pool is empty")
	if !reflect.DeepEqual(expectedErr, err) {
		t.Errorf(
			"unexpected error\n"+
				"expected: [%v]\n"+
				"actual:   [%v]",
			expectedErr,
			err,
		)
	}
}

// TestStop ensures the pool honors the stop signal send to the scheduler and it
// does not keep generating params in some internal loop.
func TestStop(t *testing.T) {
	pool, scheduler, _ := newTestPool(50000)

	// give some time to generate parameters and stop
	time.Sleep(25 * time.Millisecond)
	scheduler.stop()

	// give some time for the generation process to stop and capture the number
	// of parameters generated
	time.Sleep(10 * time.Millisecond)
	size := pool.CurrentSize()

	// wait some time and make sure no new parameters are generated
	time.Sleep(20 * time.Millisecond)
	if size != pool.CurrentSize() {
		t.Errorf("expected no new parameters to be generated")
	}
}

// TestStopNoNils ensures no nil result is added to the pool when the context
// passed to the generate function is done. The generateFn returns nil when the
// context is done and we should not add nil elements to the pool.
func TestStopNoNils(t *testing.T) {
	pool, scheduler, _ := newTestPool(50000, func(ctx context.Context) *big.Int {
		<-ctx.Done()
		return nil
	})

	// give some time to generate parameters and stop
	time.Sleep(25 * time.Millisecond)
	scheduler.stop()

	// give some time for the generation process to stop
	time.Sleep(10 * time.Millisecond)

	if pool.CurrentSize() != 0 {
		t.Errorf("expected no parameters to be generated")
	}
}

// TestPersist ensures parameters generated by the pool are persisted.
func TestPersist(t *testing.T) {
	pool, scheduler, persistence := newTestPool(50000)

	// give some time to generate parameters and stop
	time.Sleep(25 * time.Millisecond)
	scheduler.stop()

	// give some time for the generation process to stop
	time.Sleep(10 * time.Millisecond)

	if pool.CurrentSize() != persistence.parameterCount() {
		t.Errorf("not all parameters have been persisted")
	}
}

// TestReadAll ensures pool reads parameters from the persistence before
// generating new ones.
func TestReadAll(t *testing.T) {
	persistence := &mockPersistence{storage: map[string]*big.Int{
		"100": big.NewInt(100),
		"200": big.NewInt(200),
	}}

	pool, scheduler := newTestPoolWithPersistence(100, persistence)
	defer scheduler.stop()

	e, err := pool.GetNow()
	if err != nil {
		t.Errorf("unexpected error: [%v]", err)
	}
	testutils.AssertBigIntsEqual(t, "parameter value", big.NewInt(100), e)

	e, err = pool.GetNow()
	if err != nil {
		t.Errorf("unexpected error: [%v]", err)
	}
	testutils.AssertBigIntsEqual(t, "parameter value", big.NewInt(200), e)

}

// TestDelete ensures parameters fetched from the pool are deleted from the
// persistence layer.
func TestDelete(t *testing.T) {
	persistence := &mockPersistence{storage: map[string]*big.Int{
		"100": big.NewInt(100),
	}}

	pool, scheduler := newTestPoolWithPersistence(100, persistence)
	defer scheduler.stop()

	e, err := pool.GetNow()
	if err != nil {
		t.Errorf("unexpected error: [%v]", err)
	}
	if persistence.isPresent(e) {
		t.Errorf("element should be deleted from persistence: [%v]", e)
	}
}

func newTestPool(
	targetSize int,
	optionalGenerateFn ...func(context.Context) *big.Int,
) (*ParameterPool[big.Int], *Scheduler, *mockPersistence) {
	persistence := &mockPersistence{storage: make(map[string]*big.Int)}
	pool, scheduler := newTestPoolWithPersistence(
		targetSize,
		persistence,
		optionalGenerateFn...,
	)
	return pool, scheduler, persistence
}

func newTestPoolWithPersistence(
	targetSize int,
	persistence *mockPersistence,
	optionalGenerateFn ...func(context.Context) *big.Int,
) (*ParameterPool[big.Int], *Scheduler) {
	var generateFn func(context.Context) *big.Int

	if len(optionalGenerateFn) == 1 {
		generateFn = optionalGenerateFn[0]
	} else {
		generateFn = func(context.Context) *big.Int {
			time.Sleep(5 * time.Millisecond)
			return big.NewInt(time.Now().UnixMilli())
		}
	}

	scheduler := &Scheduler{}

	return NewParameterPool[big.Int](
		logger,
		scheduler,
		persistence,
		targetSize,
		generateFn,
		time.Duration(0), // no delay
	), scheduler
}

type mockPersistence struct {
	storage map[string]*big.Int
	mutex   sync.RWMutex
}

func (mp *mockPersistence) Save(element *big.Int) error {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	mp.storage[element.String()] = element
	return nil
}

func (mp *mockPersistence) Delete(element *big.Int) error {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	delete(mp.storage, element.String())
	return nil
}

func (mp *mockPersistence) ReadAll() ([]*big.Int, error) {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	all := make([]*big.Int, 0, len(mp.storage))
	for _, v := range mp.storage {
		all = append(all, v)
	}
	// sorting is needed for TestReadAll
	sort.Slice(all, func(i, j int) bool {
		return all[i].Cmp(all[j]) < 0
	})
	return all, nil
}

func (mp *mockPersistence) parameterCount() int {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	return len(mp.storage)
}

func (mp *mockPersistence) isPresent(element *big.Int) bool {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()

	_, ok := mp.storage[element.String()]
	return ok
}
