// Copyright 2016 Canonical Ltd.

// The servermon package is used to update statistics used
// for monitoring the API server.
package servermon

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ApplicationsRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "health",
		Name:      "applications_running",
		Help:      "The current number of running applications.",
	}, []string{"ctl_path"})
	AuthenticationFailCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "auth",
		Name:      "authentication_fail",
		Help:      "The number of failed authentications.",
	})
	AuthenticationSuccessCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "auth",
		Name:      "authentication_success",
		Help:      "The number of successful authentications.",
	})
	AuthenticatorPoolGet = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "auth",
		Name:      "pool_get",
		Help:      "The number of times an Authenticator has been retrieved from the pool.",
	})
	AuthenticatorPoolNew = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "auth",
		Name:      "pool_new",
		Help:      "The number of times a new Authenticator has been created by the pool.",
	})
	AuthenticatorPoolPut = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "auth",
		Name:      "pool_put",
		Help:      "The number of times an Authenticator has been replaced into the pool.",
	})
	ControllersRunning = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "health",
		Name:      "controllers_running",
		Help:      "The current number of running controllers.",
	})
	DatabaseConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "database",
		Name:      "database_connections",
		Help:      "The number of database connections in use, these are shared between database sessions.",
	})
	DatabaseSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "database",
		Name:      "database_sessions",
		Help:      "The number of database session objects, some of these will be sharing connections.",
	})
	DeployedUnitCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "deployed_unit_count",
		Help:      "The number of deployed units.",
	})
	LoginFailCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "login_fail_count",
		Help:      "The number of failed logins attempted.",
	})
	LoginRedirectCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "login_redirect_count",
		Help:      "The number of logins redirected to another controller.",
	})
	LoginSuccessCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "login_success_count",
		Help:      "The number of successful logins completed.",
	})
	MachinesRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "health",
		Name:      "machines_running",
		Help:      "The current number of running machines.",
	}, []string{"ctl_path"})
	ModelsCreatedCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "models_created_count",
		Help:      "The number of models created.",
	})
	ModelsCreatedFailCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "models_created_fail_count",
		Help:      "The number of fails attempting to create models.",
	})
	ModelsDestroyedCount = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "jem",
		Subsystem: "websocket",
		Name:      "models_destroyed_count",
		Help:      "The number of models destroyed.",
	})
	ModelsRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "health",
		Name:      "models_running",
		Help:      "The current number of running models.",
	}, []string{"ctl_path"})
	requestDuration = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: "jem",
		Subsystem: "handler",
		Name:      "request_duration",
		Help:      "The duration of a web request in seconds.",
	}, []string{"path_pattern"})
	UnitsRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "jem",
		Subsystem: "health",
		Name:      "units_running",
		Help:      "The current number of running units.",
	}, []string{"ctl_path"})
)

func init() {
	prometheus.MustRegister(ApplicationsRunning)
	prometheus.MustRegister(AuthenticationFailCount)
	prometheus.MustRegister(AuthenticationSuccessCount)
	prometheus.MustRegister(AuthenticatorPoolGet)
	prometheus.MustRegister(AuthenticatorPoolNew)
	prometheus.MustRegister(AuthenticatorPoolPut)
	prometheus.MustRegister(ControllersRunning)
	prometheus.MustRegister(DatabaseConnections)
	prometheus.MustRegister(DatabaseSessions)
	prometheus.MustRegister(DeployedUnitCount)
	prometheus.MustRegister(LoginFailCount)
	prometheus.MustRegister(LoginRedirectCount)
	prometheus.MustRegister(LoginSuccessCount)
	prometheus.MustRegister(MachinesRunning)
	prometheus.MustRegister(ModelsCreatedCount)
	prometheus.MustRegister(ModelsCreatedFailCount)
	prometheus.MustRegister(ModelsRunning)
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(UnitsRunning)
}
