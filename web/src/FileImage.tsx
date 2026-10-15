import { useCallback } from "react";

export function FileImage({
  file,
  alt,
  className,
}: {
  file: File;
  alt: string;
  className: string;
}) {
  const show = useCallback(
    (image: HTMLImageElement) => {
      const url = URL.createObjectURL(file);
      image.src = url;
      return () => URL.revokeObjectURL(url);
    },
    [file],
  );
  return <img ref={show} alt={alt} className={className} />;
}
