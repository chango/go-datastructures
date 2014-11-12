package queue

import "sync"

type waiters []*sema

func (w *waiters) get() *sema {
	if len(*w) == 0 {
		return nil
	}

	sema := (*w)[0]
	copy((*w)[0:], (*w)[1:])
	(*w)[len(*w)-1] = nil // or the zero value of T
	*w = (*w)[:len(*w)-1]
	return sema
}

func (w *waiters) put(sema *sema) {
	*w = append(*w, sema)
}

type items []interface{}

func (items *items) get(number int64) []interface{} {
	returnItems := make([]interface{}, 0, number)
	index := int64(0)
	for i := int64(0); i < number; i++ {
		if i >= int64(len(*items)) {
			break
		}

		returnItems = append(returnItems, (*items)[i])
		(*items)[i] = nil
		index++
	}

	*items = (*items)[index:]
	return returnItems
}

func (items *items) getUntil(checker func(item interface{}) bool) []interface{} {
	length := len(*items)

	if len(*items) == 0 {
		// returning nil here actually wraps that nil in a list
		// of interfaces... thanks go
		return []interface{}{}
	}

	returnItems := make([]interface{}, 0, length)
	index := 0
	for i, item := range *items {
		if !checker(item) {
			break
		}

		returnItems = append(returnItems, item)
		index = i
	}

	*items = (*items)[index:]
	return returnItems
}

type sema struct {
	wg       *sync.WaitGroup
	response *sync.WaitGroup
}

func newSema() *sema {
	return &sema{
		wg:       &sync.WaitGroup{},
		response: &sync.WaitGroup{},
	}
}

// Queue is the struct responsible for tracking the state
// of the queue.
type Queue struct {
	waiters  waiters
	items    items
	lock     sync.Mutex
	disposed bool
}

// Put will add the specified items to the queue.
func (q *Queue) Put(items ...interface{}) error {
	if len(items) == 0 {
		return nil
	}

	q.lock.Lock()

	if q.disposed {
		q.lock.Unlock()
		return DisposedError{}
	}

	q.items = append(q.items, items...)
	for {
		sema := q.waiters.get()
		if sema == nil {
			break
		}
		sema.response.Add(1)
		sema.wg.Done()
		sema.response.Wait()
		if len(q.items) == 0 {
			break
		}
	}

	q.lock.Unlock()
	return nil
}

// Get will add an item to the queue.  If there are some items in the
// queue, get will return a number UP TO the number passed in as a
// parameter.  If no items are in the queue, this method will pause
// until items are added to the queue.
func (q *Queue) Get(number int64) ([]interface{}, error) {
	if number < 1 {
		// thanks again go
		return []interface{}{}, nil
	}

	q.lock.Lock()

	if q.disposed {
		q.lock.Unlock()
		return nil, DisposedError{}
	}

	var items []interface{}

	if len(q.items) == 0 {
		sema := newSema()
		q.waiters.put(sema)
		sema.wg.Add(1)
		q.lock.Unlock()

		sema.wg.Wait()
		// we are now inside the put's lock
		if q.disposed {
			return nil, DisposedError{}
		}
		items = q.items.get(number)
		sema.response.Done()
		return items, nil
	}

	items = q.items.get(number)
	q.lock.Unlock()
	return items, nil
}

// TakeUntil takes a function and returns a list of items that
// match the checker until the checker returns false.  This does not
// wait if there are no items in the queue.
func (q *Queue) TakeUntil(checker func(item interface{}) bool) ([]interface{}, error) {
	if checker == nil {
		return nil, nil
	}

	q.lock.Lock()

	if q.disposed {
		q.lock.Unlock()
		return nil, DisposedError{}
	}

	result := q.items.getUntil(checker)
	q.lock.Unlock()
	return result, nil
}

// Empty returns a bool indicating if this bool is empty.
func (q *Queue) Empty() bool {
	q.lock.Lock()
	defer q.lock.Unlock()

	return len(q.items) == 0
}

// Len returns the number of items in this queue.
func (q *Queue) Len() int64 {
	q.lock.Lock()
	defer q.lock.Unlock()

	return int64(len(q.items))
}

// Disposed returns a bool indicating if this queue
// has had disposed called on it.
func (q *Queue) Disposed() bool {
	q.lock.Lock()
	defer q.lock.Unlock()

	return q.disposed
}

// Dispose will dispose of this queue.  Any subsequent
// calls to Get or Put will return an error.
func (q *Queue) Dispose() {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.disposed = true
	for _, waiter := range q.waiters {
		waiter.response.Add(1)
		waiter.wg.Done()
	}

	q.items = nil
	q.waiters = nil
}

// New is a constructor for a new threadsafe queue.
func New(hint int64) *Queue {
	return &Queue{
		items: make([]interface{}, 0, hint),
	}
}