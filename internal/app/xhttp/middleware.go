package xhttp

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/thecodingmachine/gotenberg/internal/app/xhttp/pkg/context"
	"github.com/thecodingmachine/gotenberg/internal/app/xhttp/pkg/resource"
	"github.com/thecodingmachine/gotenberg/internal/pkg/conf"
	"github.com/thecodingmachine/gotenberg/internal/pkg/pm2"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xerror"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xlog"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xrand"
)

// contextMiddleware extends the default echo.Context with
// our custom context.Context.
func contextMiddleware(config conf.Config, processes ...pm2.Process) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// generate a unique identifier for the request.
			trace := xrand.Get()
			// create the logger for this request using
			// the previous identifier as trace.
			logger := xlog.New(config.LogLevel(), trace)
			// extend the current echo context with our custom
			// context.
			ctx := context.New(c, logger, config, processes...)
			// if its an healthcheck request, there
			// is no need to create a Resource.
			if ctx.Path() == pingEndpoint {
				return next(ctx)
			}
			// if the endpoint is not for healthcheck, create a
			// Resource.
			if err := ctx.WithResource(trace); err != nil {
				// required to have a correct status code.
				ctx.Error(err)
				return ctx.LogRequestResult(err, false)
			}
			return next(ctx)
		}
	}
}

// loggerMiddleware logs the result of a request.
func loggerMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := context.MustCastFromEchoContext(c)
			err := next(ctx)
			// we do not want to log healthcheck requests if
			// log level is not set to DEBUG.
			isDebug := ctx.Path() == pingEndpoint
			return ctx.LogRequestResult(err, isDebug)
		}
	}
}

// cleanupMiddleware removes a resource.Resource
// at the end of a request.
func cleanupMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			const op string = "xhttp.cleanupMiddleware"
			err := next(c)
			ctx := context.MustCastFromEchoContext(c)
			if !ctx.HasResource() {
				// nothing to remove.
				return err
			}
			r := ctx.MustResource()
			// if a webhook URL has been given,
			// do not remove the resource.Resource here because
			// we don't know if the result file has been
			// generated or sent.
			if r.HasArg(resource.WebhookURLArgKey) {
				return err
			}
			// a resource.Resource is associated with our custom context.
			if resourceErr := r.Close(); resourceErr != nil {
				xerr := xerror.New(op, resourceErr)
				ctx.XLogger().ErrorOp(xerror.Op(xerr), xerr)
			}
			return err
		}
	}
}

// errorMiddleware handles errors (if any).
func errorMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := context.MustCastFromEchoContext(c)
			err := next(ctx)
			if err == nil {
				// so far so good!
				return nil
			}
			// if it's an error from echo
			// like 404 not found and so on.
			if echoHTTPErr, ok := err.(*echo.HTTPError); ok {
				return echoHTTPErr
			}
			// we log the initial error before returning
			// the HTTP error.
			errOp := xerror.Op(err)
			logger := ctx.XLogger()
			logger.ErrorOp(errOp, err)
			// handle our custom HTTP error.
			var httpErr error
			errCode := xerror.Code(err)
			errMessage := xerror.Message(err)
			switch errCode {
			case xerror.InvalidCode:
				httpErr = echo.NewHTTPError(http.StatusBadRequest, errMessage)
			case xerror.TimeoutCode:
				// TODO status
				httpErr = echo.NewHTTPError(http.StatusBadGateway, errMessage)
			default:
				httpErr = echo.NewHTTPError(http.StatusInternalServerError, errMessage)
			}
			// required to have a correct status code.
			ctx.Error(httpErr)
			return httpErr
		}
	}
}