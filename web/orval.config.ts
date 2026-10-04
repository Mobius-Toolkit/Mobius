import { defineConfig } from 'orval'

type Operation = { responses: Record<string, { content?: object }> }

export default defineConfig({
  api: {
    input: {
      target: '../api/openapi.json',
      override: {
        transformer: (spec) => {
          // The generator does not read contentSchema. It types the data of a
          // server-sent event as a string, not as the decoded JSON value.
          const input = JSON.parse(JSON.stringify(spec), (_key, value) =>
            value?.contentMediaType === 'application/json'
              ? value.contentSchema
              : value,
          )
          // The generator does not read itemSchema, and its function waits for
          // the full response body. An event stream does not end. The
          // components stay, so the event types stay.
          const paths: Record<string, Record<string, Operation>> = input.paths
          for (const item of Object.values(paths)) {
            for (const [method, operation] of Object.entries(item)) {
              const success = Object.entries(operation.responses).filter(
                ([code]) => code.startsWith('2'),
              )
              if (
                success.length > 0 &&
                success.every(
                  ([, response]) =>
                    Object.keys(response.content ?? {}).join() ===
                    'text/event-stream',
                )
              ) {
                delete item[method]
              }
            }
          }
          return input
        },
      },
    },
    output: {
      target: 'src/api/api.gen.ts',
      client: 'fetch',
      mode: 'single',
      // With request options, the generated code has a helper that Oxlint
      // refuses (unicorn/consistent-function-scoping).
      override: { requestOptions: false },
    },
  },
})
