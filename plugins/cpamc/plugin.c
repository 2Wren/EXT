/* CPA native plugin ABI v1. This library exposes only a CPAMC resource. */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    void *ptr;
    size_t len;
} buffer;

typedef int (*plugin_call)(char *, uint8_t *, size_t, buffer *);
typedef void (*plugin_free)(void *, size_t);
typedef void (*plugin_shutdown)(void);

typedef struct {
    uint32_t abi_version;
    plugin_call call;
    plugin_free free_buffer;
    plugin_shutdown shutdown;
} plugin_api;

static void release(void *ptr, size_t len) {
    (void)len;
    free(ptr);
}

static void shutdown_plugin(void) {}

static int dispatch(char *method, uint8_t *request, size_t length, buffer *out) {
    static const char metadata[] =
        "{\"ok\":true,\"result\":{\"schema_version\":1,\"metadata\":{"
        "\"Name\":\"cpa-ext\",\"Version\":\"0.1.0\",\"Author\":\"cpa-ext\","
        "\"GitHubRepository\":\"http://127.0.0.1:8482/cpa-ext/\"},"
        "\"capabilities\":{\"management_api\":true}}}";
    static const char resources[] =
        "{\"ok\":true,\"result\":{\"resources\":[{"
        "\"Path\":\"/open\",\"Menu\":\"cpa-ext\","
        "\"Description\":\"用量、Key 额度与会话审计\"}]}}";
    static const char redirect[] =
        "{\"ok\":true,\"result\":{\"StatusCode\":302,\"Headers\":{"
        "\"Location\":[\"/cpa-ext/?embed=cpamc\"],\"Cache-Control\":[\"no-store\"]}}}";
    static const char empty[] = "{\"ok\":true,\"result\":{}}";
    static const char unknown[] = "{\"ok\":false,\"error\":{\"code\":\"unknown_method\",\"message\":\"Unsupported call\"}}";
    const char *result = unknown;

    (void)request;
    (void)length;
    if (out == NULL) return 1;
    out->ptr = NULL;
    out->len = 0;
    if (method != NULL) {
        if (strcmp(method, "plugin.register") == 0 || strcmp(method, "plugin.reconfigure") == 0) result = metadata;
        else if (strcmp(method, "management.register") == 0) result = resources;
        else if (strcmp(method, "management.handle") == 0) result = redirect;
        else if (strcmp(method, "plugin.shutdown") == 0 || strcmp(method, "plugin.quiesce") == 0) result = empty;
    }
    size_t size = strlen(result);
    out->ptr = malloc(size);
    if (out->ptr == NULL) return 1;
    memcpy(out->ptr, result, size);
    out->len = size;
    return 0;
}

__attribute__((visibility("default")))
int cliproxy_plugin_init(const void *host, plugin_api *api) {
    (void)host;
    if (api == NULL) return 1;
    api->abi_version = 1;
    api->call = dispatch;
    api->free_buffer = release;
    api->shutdown = shutdown_plugin;
    return 0;
}
