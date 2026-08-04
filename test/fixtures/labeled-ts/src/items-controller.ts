// Construct 3: NestJS-style decorated controller. list() carries @Get('list')
// and the class carries @Controller('items'); the decorator-route detection
// strategy (internal/ingest/scip/api_surface.go detectDecoratorRoutes) must
// turn this into an APIRoute node {method: "GET", path: "/items/list",
// detectionSource: "decorator"} plus an EXPOSES_API edge from list() to it.
@Controller("items")
export class ItemsController {
  @Get("list")
  list(): string[] {
    return ["a", "b"];
  }
}
