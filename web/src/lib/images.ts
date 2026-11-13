import { Effect } from "effect";
import { fitted, ImageFailed } from "./effects";

export const maxImages = 4;
export const tooManyImages = `A message has at most ${maxImages} images.`;
const maxEdge = 1568;
const maxBytes = 5 * 1024 * 1024;
const types = ["image/png", "image/jpeg", "image/gif", "image/webp"];

// Gives the image, or a scaled copy when the image is outside the limits of the API. A scaled copy of a GIF has one
// frame, and a scaled copy of a WebP has no transparency.
export const fitImage = Effect.fnUntraced(function* (
  file: File,
): Effect.fn.Return<File, ImageFailed> {
  if (!types.includes(file.type)) {
    return yield* new ImageFailed({
      message: `${file.name} is not a PNG, JPEG, GIF or WebP image.`,
    });
  }
  const bitmap = yield* fitted(() => createImageBitmap(file)).pipe(
    Effect.mapError(() => new ImageFailed({ message: `${file.name} is not a valid image.` })),
  );
  const scale = Math.min(1, maxEdge / Math.max(bitmap.width, bitmap.height));
  if (scale === 1 && file.size <= maxBytes) {
    bitmap.close();
    return file;
  }
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  canvas.getContext("2d")?.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  const blob = yield* Effect.callback<Blob | null>((resume) => {
    canvas.toBlob(
      (result) => resume(Effect.succeed(result)),
      file.type === "image/png" ? "image/png" : "image/jpeg",
      0.85,
    );
  });
  if (!blob || blob.size > maxBytes) {
    return yield* new ImageFailed({ message: `${file.name} is too large.` });
  }
  return new File([blob], file.name, { type: blob.type });
});
