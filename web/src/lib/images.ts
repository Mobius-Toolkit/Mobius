export const maxImages = 4;
const maxEdge = 1568;
const maxBytes = 5 * 1024 * 1024;
const types = ["image/png", "image/jpeg", "image/gif", "image/webp"];

// Gives the image, or a scaled copy when the image is outside the limits of the API. A scaled copy of a GIF has one
// frame, and a scaled copy of a WebP has no transparency.
export async function fitImage(file: File) {
  if (!types.includes(file.type)) {
    throw new Error(`${file.name} is not a PNG, JPEG, GIF or WebP image.`);
  }
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error(`${file.name} is not a valid image.`);
  });
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
  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, file.type === "image/png" ? "image/png" : "image/jpeg", 0.85),
  );
  if (!blob || blob.size > maxBytes) {
    throw new Error(`${file.name} is too large.`);
  }
  return new File([blob], file.name, { type: blob.type });
}
