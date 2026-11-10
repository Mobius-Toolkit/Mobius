import { Option, Schema } from "effect";
import { ListRecurringTasksResponse } from "@/api/schemas.gen";

export const RecurringTask = ListRecurringTasksResponse.fields.data.fields.tasks.value;
export type RecurringTask = typeof RecurringTask.Type;

export const Deletion = Schema.Struct({
  id: Schema.Number,
  text: Schema.String,
  busy: Schema.Boolean,
});

export type Deletion = typeof Deletion.Type;

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  tasks: Schema.Option(Schema.Array(RecurringTask)),
  error: Schema.String,
  opened: Schema.Option(Schema.Number),
  scroll: Schema.Number,
  busy: Schema.Array(Schema.Number),
  running: Schema.Array(Schema.Number),
  deletion: Schema.Option(Deletion),
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(options: {
  owner: string;
  name: string;
  number: number;
  instance: string;
}): Model {
  return {
    ...options,
    tasks: Option.none(),
    error: "",
    opened: Option.none(),
    scroll: 0,
    busy: [],
    running: [],
    deletion: Option.none(),
  };
}
