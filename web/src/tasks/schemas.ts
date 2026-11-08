import { ListBoardTasksResponse } from "@/api/schemas.gen";

export const BoardTask = ListBoardTasksResponse.fields.data.value;

export type BoardTask = typeof BoardTask.Type;
