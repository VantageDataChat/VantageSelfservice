package middleware

import "net/http"

// Middleware 定义中间件函数类型。
// 接收一个 http.HandlerFunc 并返回一个包装后的 http.HandlerFunc。
type Middleware func(http.HandlerFunc) http.HandlerFunc

// Chain 将多个中间件按顺序组合，返回一个组合后的中间件。
// 执行顺序遵循洋葱模型：Chain(m1, m2, ..., mn) 的执行顺序为
// m1 → m2 → ... → mn → handler → mn → ... → m2 → m1。
// 即第一个参数为最外层，最后一个参数为最内层。
//
// 如果没有传入任何中间件，返回一个透传中间件（直接返回原始 handler）。
func Chain(middlewares ...Middleware) Middleware {
	return func(final http.HandlerFunc) http.HandlerFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
