import { use, useEffect, useState } from "react";
import { getVoiceLanguage, setVoiceLanguage, type VoiceLanguage } from "@/api/api.gen";
import { LoginContext } from "./login";

export function useVoiceLanguage() {
  const showLogin = use(LoginContext);
  const [voice, setVoice] = useState<VoiceLanguage>();
  const [error, setError] = useState("");

  useEffect(() => {
    getVoiceLanguage()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setVoice((current) => current ?? res.data.data);
        } else {
          setError(`Mobius could not load the language: ${res.data.error}.`);
        }
      })
      .catch((err: unknown) => setError(`Mobius could not load the language: ${String(err)}.`));
  }, [showLogin]);

  const select = async (code: string) => {
    try {
      const res = await setVoiceLanguage({ language: code });
      if (res.status === 200) {
        setVoice(res.data.data);
        setError("");
      } else if (res.status === 401) {
        showLogin();
      } else {
        setError(`Mobius could not save the language: ${res.data.error}.`);
      }
    } catch (err) {
      setError(`Mobius could not save the language: ${String(err)}.`);
    }
  };

  return { voice, error, select };
}
