import { ref } from "vue";

// undefined until the first check answers, null when nobody is signed in.
export const currentUser = ref<string | null | undefined>(undefined);
