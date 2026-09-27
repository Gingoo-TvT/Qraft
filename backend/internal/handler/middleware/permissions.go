package middleware

import (
	"github.com/labstack/echo/v4"
	"net/http"
)

type RoutePermission string

const (
	PermissionPublic RoutePermission = "public"
	PermissionMember RoutePermission = "member"
	PermissionAdmin  RoutePermission = "admin"
)

// Permissions are exact HTTP method + registered Echo pattern, never a URL
// prefix. Resource-specific ownership checks remain in the corresponding service.
var routePermissions = map[string]RoutePermission{
	"GET /health":                                                   PermissionPublic,
	"GET /api/v1/auth/session":                                      PermissionPublic,
	"GET /api/v1/auth/invitation":                                   PermissionPublic,
	"POST /api/v1/auth/login":                                       PermissionPublic,
	"POST /api/v1/auth/register":                                    PermissionPublic,
	"POST /api/v1/auth/reset-password":                              PermissionPublic,
	"POST /api/v1/auth/bootstrap":                                   PermissionPublic,
	"GET /api/v1/public/rating/review":                              PermissionPublic,
	"POST /api/v1/public/rating/review":                             PermissionPublic,
	"POST /api/v1/auth/logout":                                      PermissionMember,
	"POST /api/v1/auth/password":                                    PermissionMember,
	"GET /api/v1/integration/capabilities":                          PermissionMember,
	"GET /api/v1/settings/generation-readiness":                     PermissionMember,
	"POST /api/v1/sources/preview":                                  PermissionMember,
	"POST /api/v1/problem-imports":                                  PermissionMember,
	"POST /api/v1/problem-imports/:id/resume":                       PermissionMember,
	"GET /api/v1/problem-imports/:id":                               PermissionMember,
	"POST /api/v1/problems/generate":                                PermissionMember,
	"POST /api/v1/problems/gplt/generate":                           PermissionMember,
	"GET /api/v1/problems":                                          PermissionMember,
	"GET /api/v1/problems/similar":                                  PermissionMember,
	"GET /api/v1/problems/hydro.zip":                                PermissionMember,
	"POST /api/v1/problems/hydro/validate":                          PermissionMember,
	"GET /api/v1/problems/:id":                                      PermissionMember,
	"GET /api/v1/problems/:id/testcases":                            PermissionMember,
	"GET /api/v1/problems/:id/test-manifest":                        PermissionMember,
	"GET /api/v1/problems/:id/standard-evidence":                    PermissionMember,
	"GET /api/v1/problems/:id/testcases/:tid/input":                 PermissionMember,
	"GET /api/v1/problems/:id/testcases/:tid/output":                PermissionMember,
	"GET /api/v1/problems/:id/testdata.zip":                         PermissionMember,
	"GET /api/v1/problems/:id/metadata":                             PermissionMember,
	"GET /api/v1/problems/:id/editorial":                            PermissionMember,
	"GET /api/v1/problems/:id/solutions":                            PermissionMember,
	"GET /api/v1/problems/:id/hydro.zip":                            PermissionMember,
	"GET /api/v1/problems/:id/quality":                              PermissionMember,
	"POST /api/v1/problems/:id/validate":                            PermissionMember,
	"GET /api/v1/questions/search":                                  PermissionMember,
	"GET /api/v1/quizzes":                                           PermissionMember,
	"POST /api/v1/quizzes/generate":                                 PermissionMember,
	"GET /api/v1/quizzes/export":                                    PermissionMember,
	"GET /api/v1/quizzes/template.xlsx":                             PermissionMember,
	"GET /api/v1/quizzes/:id":                                       PermissionMember,
	"GET /api/v1/knowledge-points":                                  PermissionMember,
	"GET /api/v1/tags":                                              PermissionMember,
	"GET /api/v1/workflows":                                         PermissionMember,
	"GET /api/v1/workflows/:id":                                     PermissionMember,
	"GET /api/v1/workflows/:id/events":                              PermissionMember,
	"POST /api/v1/workflows/:id/approve":                            PermissionMember,
	"POST /api/v1/workflows/:id/reject":                             PermissionMember,
	"POST /api/v1/workflows/:id/retry":                              PermissionMember,
	"DELETE /api/v1/workflows/:id":                                  PermissionMember,
	"GET /api/v1/stats":                                             PermissionMember,
	"POST /api/v1/generation/jobs":                                  PermissionMember,
	"GET /api/v1/generation/jobs/:id":                               PermissionMember,
	"GET /api/v1/generation/jobs/:id/result":                        PermissionMember,
	"DELETE /api/v1/generation/jobs/:id":                            PermissionMember,
	"POST /api/v1/generation/micro-batches":                         PermissionMember,
	"GET /api/v1/generation/micro-batches/:id":                      PermissionMember,
	"GET /api/v1/generation/micro-batches/:id/result":               PermissionMember,
	"DELETE /api/v1/generation/micro-batches/:id":                   PermissionMember,
	"GET /api/v1/generation/jobs/:id/events":                        PermissionMember,
	"POST /api/v1/problem-sets":                                     PermissionMember,
	"GET /api/v1/problem-sets":                                      PermissionMember,
	"POST /api/v1/problem-sets/assembly-preview":                    PermissionMember,
	"POST /api/v1/problem-sets/assemble":                            PermissionMember,
	"GET /api/v1/problem-sets/:id":                                  PermissionMember,
	"PUT /api/v1/problem-sets/:id":                                  PermissionMember,
	"DELETE /api/v1/problem-sets/:id":                               PermissionMember,
	"POST /api/v1/problem-sets/:id/items/batch":                     PermissionMember,
	"POST /api/v1/problem-sets/:id/items":                           PermissionMember,
	"PUT /api/v1/problem-sets/:id/items/reorder":                    PermissionMember,
	"DELETE /api/v1/problem-sets/:id/items/:item_id":                PermissionMember,
	"GET /api/v1/problem-sets/:id/quality":                          PermissionMember,
	"POST /api/v1/problem-sets/:id/generate-prompt":                 PermissionMember,
	"POST /api/v1/problem-sets/:id/export.zip":                      PermissionMember,
	"GET /api/v1/problem-sets/:id/export.zip":                       PermissionMember,
	"POST /api/v1/problem-sets/:id/generation":                      PermissionMember,
	"GET /api/v1/problem-sets/:id/generation":                       PermissionMember,
	"POST /api/v1/problem-sets/:id/generation/cancel":               PermissionMember,
	"GET /api/v1/admin/users":                                       PermissionAdmin,
	"PATCH /api/v1/admin/users/:id":                                 PermissionAdmin,
	"GET /api/v1/admin/invitations":                                 PermissionAdmin,
	"POST /api/v1/admin/invitations":                                PermissionAdmin,
	"DELETE /api/v1/admin/invitations/:id":                          PermissionAdmin,
	"PUT /api/v1/problems/:id":                                      PermissionAdmin,
	"POST /api/v1/problems/:id/edit-refresh":                        PermissionAdmin,
	"POST /api/v1/problems/:id/public-release-approval":             PermissionAdmin,
	"DELETE /api/v1/problems/:id":                                   PermissionAdmin,
	"GET /api/v1/problems/:id/quality/audit":                        PermissionAdmin,
	"POST /api/v1/problems/quality/batch-manifest":                  PermissionAdmin,
	"POST /api/v1/quizzes":                                          PermissionAdmin,
	"POST /api/v1/quizzes/import":                                   PermissionAdmin,
	"PUT /api/v1/quizzes/:id":                                       PermissionAdmin,
	"DELETE /api/v1/quizzes/:id":                                    PermissionAdmin,
	"GET /api/v1/settings/llm":                                      PermissionAdmin,
	"PUT /api/v1/settings/llm/:purpose":                             PermissionAdmin,
	"DELETE /api/v1/settings/llm/:purpose":                          PermissionAdmin,
	"GET /api/v1/settings/review":                                   PermissionAdmin,
	"PUT /api/v1/settings/review":                                   PermissionAdmin,
	"GET /api/v1/embedding/status":                                  PermissionAdmin,
	"GET /api/v1/embedding/models":                                  PermissionAdmin,
	"GET /api/v1/embedding/runtime-settings":                        PermissionAdmin,
	"GET /api/v1/embedding/saved-runtime-settings":                  PermissionAdmin,
	"POST /api/v1/embedding/local/test":                             PermissionAdmin,
	"POST /api/v1/embedding/local/deploy":                           PermissionAdmin,
	"POST /api/v1/embedding/local/backfill":                         PermissionAdmin,
	"POST /api/v1/embedding/local/activate":                         PermissionAdmin,
	"GET /api/v1/rating/problems/:id":                               PermissionAdmin,
	"POST /api/v1/rating/problems/:id/assessments":                  PermissionAdmin,
	"GET /api/v1/rating/assessments/:id":                            PermissionAdmin,
	"DELETE /api/v1/rating/assessments/:id":                         PermissionAdmin,
	"GET /api/v1/rating/anchors":                                    PermissionAdmin,
	"POST /api/v1/rating/anchors":                                   PermissionAdmin,
	"GET /api/v1/rating/problems/:id/invitations":                   PermissionAdmin,
	"POST /api/v1/rating/problems/:id/invitations":                  PermissionAdmin,
	"DELETE /api/v1/rating/problems/:id/invitations/:invitation_id": PermissionAdmin,
	"GET /api/v1/rating/problems/:id/feedback":                      PermissionAdmin,
	"POST /api/v1/rating/problems/:id/calibration":                  PermissionAdmin,
	"GET /api/v1/rating/problems/:id/decisions":                     PermissionAdmin,
	"POST /api/v1/rating/problems/:id/decisions":                    PermissionAdmin,
}

func PermissionForRoute(method, pattern string) (RoutePermission, bool) {
	permission, ok := routePermissions[method+" "+pattern]
	return permission, ok
}

// RouteAuthorization fails closed for newly added endpoints until their role
// boundary is explicitly chosen. It must run after SessionAuth.
func RouteAuthorization() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Path() == "" {
				return next(c)
			} // preserve normal 404 responses
			permission, known := PermissionForRoute(c.Request().Method, c.Path())
			if !known {
				return c.JSON(http.StatusForbidden, map[string]any{"success": false, "error": map[string]string{"code": "ROUTE_PERMISSION_REQUIRED", "message": "this endpoint has no configured permission"}})
			}
			if permission == PermissionPublic {
				return next(c)
			}
			claims := GetClaims(c)
			if claims == nil || claims.UserID == "" {
				return c.JSON(http.StatusUnauthorized, map[string]any{"success": false, "error": map[string]string{"code": "UNAUTHENTICATED", "message": "sign in to continue"}})
			}
			if claims.Role != "admin" && (claims.Role != "member" || permission == PermissionAdmin) {
				return c.JSON(http.StatusForbidden, map[string]any{"success": false, "error": map[string]string{"code": "FORBIDDEN", "message": "administrator role is required"}})
			}
			return next(c)
		}
	}
}
