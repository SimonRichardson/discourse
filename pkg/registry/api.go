package registry

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/SimonRichardson/alchemy/pkg/api"
	"github.com/SimonRichardson/alchemy/pkg/cluster"
	"github.com/SimonRichardson/alchemy/pkg/cluster/members"
	"github.com/SimonRichardson/alchemy/pkg/cluster/registry"
	"github.com/SimonRichardson/alchemy/pkg/metrics"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

// These are the registry API URL paths.
const (
	APIPathServicesQuery = "/services"
)

const (
	defaultContentType = "application/json"
)

// API wraps a registry and provides a basic HTTP API.
type API struct {
	handler        http.Handler
	peer           cluster.Peer
	registry       registry.Registry
	tickerDuration time.Duration
	stop           chan chan struct{}
	logger         log.Logger
	clients        metrics.Gauge
	duration       metrics.HistogramVec
	errors         api.Error
}

// NewAPI creates a API with the correct dependencies.
// The API is an http.Handler and can ServeHTTP.
//
//     GET /services
//         Returns the current list of all services according to the registry.
//
//     GET /services?type={type}
//         Returns the current list of services according to the registry that
//         correspond to the type.
//         Returns 400 Bad Request if the type is in an invalid format.
//         Returns 404 Not Found if the type doesn't exist.
//
func NewAPI(peer cluster.Peer,
	registry registry.Registry,
	tickerDuration time.Duration,
	logger log.Logger,
	clients metrics.Gauge,
	duration metrics.HistogramVec,
) *API {
	api := &API{
		peer:           peer,
		registry:       registry,
		tickerDuration: tickerDuration,
		stop:           make(chan chan struct{}),
		logger:         logger,
		clients:        clients,
		duration:       duration,
		errors:         api.NewError(logger),
	}
	{
		router := mux.NewRouter().StrictSlash(true)
		router.Methods("GET").Path(APIPathServicesQuery).HandlerFunc(api.handleServices)
		router.NotFoundHandler = http.HandlerFunc(api.errors.NotFound)
		api.handler = router
	}
	return api
}

func (a *API) Run() error {
	adapter := eventAdapter{registry: a.registry}
	if err := a.peer.RegisterEventHandler(adapter); err != nil {
		return err
	}

	ticker := time.NewTicker(a.tickerDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// TODO (Simon) - walk through the peer nodes and make a change set

		case c := <-a.stop:
			defer close(c)

			if err := a.peer.DeregisterEventHandler(adapter); err != nil {
				return err
			}
		}
	}
}

// Stop and close out the API
func (a *API) Stop() {
	c := make(chan struct{})
	a.stop <- c
	<-c
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	level.Info(a.logger).Log("method", r.Method, "url", r.URL.String())

	iw := &interceptingWriter{http.StatusOK, w}
	w = iw

	// Metrics
	a.clients.Inc()
	defer a.clients.Dec()

	defer func(begin time.Time) {
		a.duration.WithLabelValues(
			r.Method,
			r.URL.Path,
			strconv.Itoa(iw.code),
		).Observe(time.Since(begin).Seconds())
	}(time.Now())

	a.handler.ServeHTTP(w, r)
}

func (a *API) handleServices(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	// useful metrics
	begin := time.Now()

	// validate input
	var params ServicesParams
	if err := params.DecodeFrom(r.Header, r.URL.Query()); err != nil {
		a.errors.BadRequest(w, r, err.Error())
		return
	}

	services, err := a.peer.Current(params.Type)
	if err != nil {
		a.errors.InternalServerError(w, r, err.Error())
		return
	}

	if params.Type != cluster.PeerTypeAny {
		if list, ok := services[params.Type]; !ok || len(list) == 0 {
			a.errors.NotFound(w, r)
			return
		}
	}

	result := ServicesResult{Errors: a.errors, Params: params}
	result.Services = services

	// Finish
	result.Duration = time.Since(begin).String()
	result.EncodeTo(w)
}

// ServicesParams handles
type ServicesParams struct {
	Type members.PeerType
}

// DecodeFrom populates a ServicesParams from a Request.
func (p *ServicesParams) DecodeFrom(headers http.Header, values url.Values) (err error) {
	if accept := headers.Get("Accept"); accept != "" && accept != defaultContentType {
		return errors.Errorf("expected %q content-type, got %q", defaultContentType, accept)
	}

	if typ := values.Get("type"); typ == "" {
		p.Type = cluster.PeerTypeAny
	} else {
		p.Type, err = cluster.ParsePeerType(typ)
	}
	return
}

// ServicesResult contains statistics about the services query.
type ServicesResult struct {
	Errors   api.Error
	Params   ServicesParams
	Duration string
	Services map[members.PeerType][]string
}

// EncodeTo encodes the Services to the HTTP response
// writer.
func (r *ServicesResult) EncodeTo(w http.ResponseWriter) {
	headers := w.Header()
	headers.Set(httpHeaderContentType, defaultContentType)
	headers.Set(httpHeaderDuration, r.Duration)
	headers.Set(httpHeaderType, r.Params.Type.String())

	if err := json.NewEncoder(w).Encode(struct {
		Services map[members.PeerType][]string `json:"services"`
	}{
		Services: r.Services,
	}); err != nil {
		r.Errors.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

const (
	httpHeaderContentType = "Content-Type"
	httpHeaderDuration    = "X-Duration"
	httpHeaderType        = "X-Type"
)

type eventAdapter struct {
	registry registry.Registry
}

func (e eventAdapter) HandleEvent(event members.Event) error {
	if event.Type() == members.EventMember {
		memberEvent, ok := event.(members.MemberEvent)
		if !ok {
			return nil
		}

		var fn func(registry.Key) bool
		switch memberEvent.EventType {
		case members.EventMemberJoined:
			fn = e.registry.Add
		case members.EventMemberLeft:
			fn = e.registry.Remove
		case members.EventMemberUpdated:
			fn = e.registry.Update
		}

		if fn == nil {
			return nil
		}

		keys := membersToKeys(memberEvent.Members)
		for _, v := range keys {
			fn(v)
		}
	}
	return nil
}

func membersToKeys(m []members.Member) []registry.Key {
	res := make([]registry.Key, len(m))
	for k, v := range m {
		res[k] = registry.NewMemberKey(v)
	}
	return res
}
