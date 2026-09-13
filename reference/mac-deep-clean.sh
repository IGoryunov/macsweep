#!/bin/bash
# mac-deep-clean.sh — глубокая ревизия места на macOS.
#
#   ./mac-deep-clean.sh              отчёт, ничего не трогает
#   ./mac-deep-clean.sh --clean      перенос отмеченного в Корзину, с подтверждением
#   ./mac-deep-clean.sh --rescan     пересчитать заново (иначе берётся кэш до 2 часов)
#
# Профиль: игры и Windows-прослойки не нужны, локальные LLM не нужны,
# мобильной разработки нет, режим максимальный.
#
# Ничего не удаляется через rm. Всё уезжает в ~/.Trash/deep-clean-<дата>/.

set -u
export LC_ALL=C

HOME_DIR="$HOME"
AS="$HOME/Library/Application Support"
CA="$HOME/Library/Caches"
CT="$HOME/Library/Containers"
GC="$HOME/Library/Group Containers"
DEV="$HOME/Library/Developer"

MANIFEST="/tmp/mac-deep-clean-$(id -u).tsv"
MIN_KB=51200          # порог показа: 50 МБ
STALE_DAYS=60         # "неактивный проект" — не менялся столько дней

CLEAN=0; RESCAN=0
for a in "$@"; do
  case "$a" in
    --clean)  CLEAN=1 ;;
    --rescan) RESCAN=1 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "неизвестный флаг: $a"; exit 1 ;;
  esac
done

b=$'\033[1m'; d=$'\033[2m'; g=$'\033[32m'; y=$'\033[33m'; r=$'\033[31m'; c=$'\033[36m'; n=$'\033[0m'

human() {
  awk -v k="${1:-0}" 'BEGIN{
    if (k>=1048576) printf "%.1f ГБ", k/1048576;
    else if (k>=1024) printf "%.0f МБ", k/1024;
    else printf "%d КБ", k}'
}
kb() { [ -e "$1" ] || { echo 0; return; }; du -sk "$1" 2>/dev/null | awk 'NR==1{print $1}'; }
tilde() { local t='~'; echo "${1/#$HOME_DIR/$t}"; }
progress() { printf "\r${d}  сканирую: %-58s${n}" "$1" >&2; }

# add SAFE GROUP PATH NOTE  — SAFE=1 значит "уедет в Корзину в режиме --clean"
add() {
  local safe="$1" grp="$2" path="$3" note="${4:-}"
  [ -e "$path" ] || return 0
  progress "$(basename "$path")"
  local size; size=$(kb "$path")
  [ "${size:-0}" -lt "$MIN_KB" ] && return 0
  printf "%s\t%s\t%s\t%s\t%s\n" "$safe" "$grp" "$size" "$path" "$note" >> "$MANIFEST"
}

app_installed() { # 1 = приложение с таким именем есть
  [ -d "/Applications/$1.app" ] || [ -d "$HOME/Applications/$1.app" ] \
    || [ -d "$HOME/Applications/JetBrains Toolbox/$1.app" ]
}
proc_running() { pgrep -qx "$1" 2>/dev/null; }

# ======================= СКАН =======================
scan() {
  : > "$MANIFEST"
  echo "${d}Считаю. Первый раз занимает 2-5 минут — du идёт по всему домашнему каталогу.${n}" >&2

  # ---- 1. Игры и Windows-прослойки: под нож целиком ----
  add 1 "Игры и Windows" "$AS/Steam"                          "клиент Steam целиком"
  add 1 "Игры и Windows" "$HOME/SteamLibrary"                 "библиотека игр"
  add 1 "Игры и Windows" "$AS/CrossOver"                      "бутылки CrossOver"
  add 1 "Игры и Windows" "$AS/com.isaacmarovitz.Whisky"       "префиксы Whisky"
  add 1 "Игры и Windows" "$AS/Battle.net"                     ""
  add 1 "Игры и Windows" "$AS/Blizzard Entertainment"         ""
  add 1 "Игры и Windows" "$CA/com.valvesoftware.steam"        ""
  add 1 "Игры и Windows" "$CA/com.codeweavers.CrossOver"      ""
  add 1 "Игры и Windows" "$HOME/Library/Logs/Steam"           ""
  for app in Steam CrossOver Whisky "Battle.net" "Wine Staging"; do
    add 0 "Игры и Windows" "/Applications/$app.app"           "само приложение — перетащите в Корзину"
  done

  # ---- 2. Локальные модели: под нож целиком ----
  add 1 "Локальные модели" "$HOME/.ollama/models"             "модели Ollama"
  add 1 "Локальные модели" "$HOME/.lmstudio"                  "LM Studio целиком"
  add 1 "Локальные модели" "$AS/Jan"                          "Jan + модели GGUF"
  add 1 "Локальные модели" "$AS/com.prakashjoshipax.VoiceInk" "модели Whisper"
  add 1 "Локальные модели" "$HOME/VoiceInk"                   ""
  add 1 "Локальные модели" "$HOME/VoiceInk-Dependencies"      ""
  add 1 "Локальные модели" "$HOME/.cache/huggingface"         "кэш HuggingFace"
  add 1 "Локальные модели" "$HOME/.cache/lm-studio"           ""
  add 1 "Локальные модели" "$AS/com.electron.jan"             ""
  for app in Ollama "LM Studio" Jan VoiceInk; do
    add 0 "Локальные модели" "/Applications/$app.app"          "само приложение"
  done

  # ---- 3. Мобильная разработка: не нужна ----
  add 1 "Мобильная разработка" "$DEV/Xcode/DerivedData"                ""
  add 1 "Мобильная разработка" "$DEV/Xcode/iOS DeviceSupport"          "symbols старых iOS"
  add 1 "Мобильная разработка" "$DEV/Xcode/watchOS DeviceSupport"      ""
  add 1 "Мобильная разработка" "$DEV/Xcode/tvOS DeviceSupport"         ""
  add 1 "Мобильная разработка" "$DEV/Xcode/Archives"                   "архивы сборок"
  add 1 "Мобильная разработка" "$DEV/Xcode/Products"                   ""
  add 1 "Мобильная разработка" "$DEV/CoreSimulator/Devices"            "все симуляторы"
  add 1 "Мобильная разработка" "$DEV/CoreSimulator/Caches"             ""
  add 1 "Мобильная разработка" "$DEV/XCTestDevices"                    ""
  add 1 "Мобильная разработка" "$CA/com.apple.dt.Xcode"                ""
  add 1 "Мобильная разработка" "$HOME/Library/Android/sdk/system-images" "образы эмуляторов"
  add 1 "Мобильная разработка" "$HOME/Library/Android/sdk/ndk"         "NDK"
  add 1 "Мобильная разработка" "$HOME/Library/Android/sdk/sources"     ""
  add 1 "Мобильная разработка" "$HOME/.android/avd"                    "виртуальные устройства"
  add 1 "Мобильная разработка" "$HOME/.konan"                          "Kotlin/Native"
  add 1 "Мобильная разработка" "$HOME/.skiko"                          ""
  add 1 "Мобильная разработка" "$HOME/.javacpp"                        ""
  add 1 "Мобильная разработка" "$HOME/.hawtjni"                        ""
  add 0 "Мобильная разработка" "$HOME/Library/Android/sdk"             "весь Android SDK — если Android совсем не нужен"
  add 0 "Мобильная разработка" "/Applications/Xcode.app"               "сам Xcode — самый крупный единичный кусок"
  add 0 "Мобильная разработка" "/Applications/Android Studio.app"      ""

  # ---- 4. JetBrains ----
  add 1 "JetBrains" "$CA/JetBrains"                "индексы — пересоберутся при открытии проекта"
  add 1 "JetBrains" "$HOME/Library/Logs/JetBrains" ""
  add 1 "JetBrains" "$HOME/IdeaSnapshots"          "профайлер-снапшоты"
  add 1 "JetBrains" "$HOME/.gradle/caches"         ""
  add 1 "JetBrains" "$HOME/.gradle/wrapper"        "дистрибутивы Gradle"
  add 0 "JetBrains" "$HOME/.m2/repository"         "Maven — понадобится офлайн-сборке"
  # осиротевшие папки конфигов по версиям
  if [ -d "$AS/JetBrains" ]; then
    while IFS= read -r vd; do
      [ -d "$vd" ] || continue
      base=$(basename "$vd")
      case "$base" in Toolbox|consentOptions|*.json) continue ;; esac
      # IntelliJIdea2023.2 -> IntelliJ IDEA ; PyCharm2024.1 -> PyCharm
      name=$(echo "$base" | sed -E 's/[0-9]{4}\.[0-9]+$//')
      installed=0
      case "$name" in
        IntelliJIdea|IdeaIC) app_installed "IntelliJ IDEA" && installed=1 ;;
        PyCharm|PyCharmCE)   app_installed "PyCharm" && installed=1 ;;
        WebStorm)            app_installed "WebStorm" && installed=1 ;;
        GoLand)              app_installed "GoLand" && installed=1 ;;
        DataGrip)            app_installed "DataGrip" && installed=1 ;;
        CLion)               app_installed "CLion" && installed=1 ;;
        RustRover)           app_installed "RustRover" && installed=1 ;;
        Rider)               app_installed "Rider" && installed=1 ;;
        PhpStorm)            app_installed "PhpStorm" && installed=1 ;;
        RubyMine)            app_installed "RubyMine" && installed=1 ;;
        AndroidStudio)       app_installed "Android Studio" && installed=1 ;;
        *) installed=1 ;;
      esac
      if [ "$installed" -eq 0 ]; then
        add 1 "JetBrains" "$vd" "IDE не установлена — осиротевший конфиг"
      else
        add 0 "JetBrains" "$vd" "IDE на месте: внутри плагины и настройки"
      fi
    done < <(find "$AS/JetBrains" -maxdepth 1 -mindepth 1 -type d 2>/dev/null)
  fi

  # ---- 5. Браузеры и Electron ----
  add 1 "Браузеры и Electron" "$CA/Google/Chrome"                  "дисковый кэш Chrome"
  add 1 "Браузеры и Electron" "$AS/Google/Chrome/Snapshots"        "копии старых версий браузера"
  add 1 "Браузеры и Electron" "$AS/GoogleUpdater"                  "загруженные обновления"
  if [ -d "$AS/Google/Chrome" ]; then
    while IFS= read -r p; do
      for sub in "Code Cache" "GPUCache" "Service Worker/CacheStorage" "Service Worker/ScriptCache" \
                 "Application Cache" "DawnGraphiteCache" "DawnWebGPUCache" "GrShaderCache" "Shared Dictionary"; do
        add 1 "Браузеры и Electron" "$p/$sub" "профиль $(basename "$p")"
      done
      add 0 "Браузеры и Electron" "$p/File System"  "данные сайтов профиля $(basename "$p")"
      add 0 "Браузеры и Electron" "$p/IndexedDB"    "данные сайтов профиля $(basename "$p")"
    done < <(find "$AS/Google/Chrome" -maxdepth 1 -type d \( -name Default -o -name "Profile *" \) 2>/dev/null)
  fi
  for br in Firefox BraveSoftware "Microsoft Edge" Arc Vivaldi Opera; do
    add 1 "Браузеры и Electron" "$CA/$br" "кэш браузера"
  done
  for app in Claude Cursor Notion Postman Lens Slack discord Figma Spotify \
             "Microsoft Teams" zoom.us Obsidian Insomnia Beekeeper-Studio \
             "Google Drive" "Docker Desktop" Antigravity Windsurf; do
    for sub in "Cache" "Code Cache" "GPUCache" "DawnGraphiteCache" "DawnWebGPUCache" \
               "Crashpad" "logs" "Logs" "Service Worker/CacheStorage" "Partitions"; do
      add 1 "Браузеры и Electron" "$AS/$app/$sub" "$app"
    done
    add 1 "Браузеры и Electron" "$CA/$app" "$app"
  done
  # кэши внутри песочниц (Telegram, Slack из App Store и т.п.)
  while IFS= read -r p; do
    add 1 "Браузеры и Electron" "$p" "песочница: $(echo "$p" | sed -E 's|.*/Containers/([^/]+)/.*|\1|')"
  done < <(find "$CT" -maxdepth 5 -type d -name Caches -path "*/Data/Library/Caches" 2>/dev/null | head -40)
  while IFS= read -r p; do
    add 1 "Браузеры и Electron" "$p" "группа: $(echo "$p" | sed -E 's|.*/Group Containers/([^/]+)/.*|\1|')"
  done < <(find "$GC" -maxdepth 4 -type d -name Caches 2>/dev/null | head -40)

  # ---- 6. Пакетные менеджеры и тулчейны ----
  add 1 "Пакеты и тулчейны" "$HOME/.npm/_cacache"          ""
  add 1 "Пакеты и тулчейны" "$CA/pnpm"                     ""
  add 1 "Пакеты и тулчейны" "$HOME/Library/pnpm/store"     "стор pnpm — пересоберётся"
  add 1 "Пакеты и тулчейны" "$HOME/.pnpm-state"            ""
  add 1 "Пакеты и тулчейны" "$CA/Yarn"                     ""
  add 1 "Пакеты и тулчейны" "$HOME/.yarn/berry/cache"      ""
  add 1 "Пакеты и тулчейны" "$CA/Homebrew"                 "скачанные бутылки brew"
  add 1 "Пакеты и тулчейны" "$HOME/Library/Caches/pip"     ""
  add 1 "Пакеты и тулчейны" "$HOME/.pip/cache"             ""
  add 1 "Пакеты и тулчейны" "$HOME/.cache"                 "сборный кэш CLI-инструментов"
  add 1 "Пакеты и тулчейны" "$HOME/.node-gyp"              ""
  add 1 "Пакеты и тулчейны" "$HOME/.nuget/packages"        ""
  add 1 "Пакеты и тулчейны" "$HOME/.cargo/registry"        ""
  add 1 "Пакеты и тулчейны" "$HOME/go/pkg/mod"             ""
  add 1 "Пакеты и тулчейны" "$HOME/.terraform.d/plugin-cache" ""
  add 1 "Пакеты и тулчейны" "$HOME/.platformio/.cache"     ""
  add 1 "Пакеты и тулчейны" "$HOME/.swiftpm/cache"         ""
  add 1 "Пакеты и тулчейны" "$CA/CocoaPods"                ""
  add 1 "Пакеты и тулчейны" "$CA/org.swift.swiftpm"        ""
  add 0 "Пакеты и тулчейны" "$HOME/.nvm/versions"          "версии Node — оставьте нужные"
  add 0 "Пакеты и тулчейны" "$HOME/.sdkman/candidates"     "JDK и прочее — оставьте нужные"
  add 0 "Пакеты и тулчейны" "$HOME/.vscode/extensions"     ""
  add 0 "Пакеты и тулчейны" "$HOME/.cursor/extensions"     ""
  add 0 "Пакеты и тулчейны" "$HOME/.platformio"            "весь PlatformIO"

  # ---- 7. Docker ----
  while IFS= read -r p; do
    add 0 "Docker" "$p" "образ ВМ: сначала docker system prune -a, потом сжать в Docker Desktop"
  done < <(find "$CT/com.docker.docker" -maxdepth 6 -name "Docker.raw" 2>/dev/null)
  add 1 "Docker" "$CA/com.docker.docker" ""
  add 0 "Docker" "$HOME/.docker" "конфиги и контексты"
  while IFS= read -r p; do
    add 0 "Docker" "$p" "данные Colima/Lima"
  done < <(find "$HOME/.colima" "$HOME/.lima" -maxdepth 1 -mindepth 1 -type d 2>/dev/null)

  # ---- 8. node_modules и build-артефакты в проектах ----
  PROJ_ROOTS="$HOME/Work $HOME/Scripts $HOME/agent $HOME/sdk $HOME/PyCharmMiscProject $HOME/Documents $HOME/Desktop"
  for root in $PROJ_ROOTS; do
    [ -d "$root" ] || continue
    while IFS= read -r p; do
      age=$(( ( $(date +%s) - $(stat -f %m "$p" 2>/dev/null || echo 0) ) / 86400 ))
      if [ "$age" -ge "$STALE_DAYS" ]; then
        add 1 "Проекты" "$p" "не менялось $age дн. — npm i вернёт"
      else
        add 0 "Проекты" "$p" "активно ($age дн. назад) — restore одной командой"
      fi
    done < <(find "$root" -type d \( -name node_modules -o -name .venv -o -name venv \
              -o -name target -o -name .next -o -name .nuxt -o -name .gradle \
              -o -name __pycache__ -o -name .pytest_cache -o -name .mypy_cache \) \
              -prune 2>/dev/null)
  done

  # ---- 9. Система и мусор ----
  add 1 "Система и мусор" "$HOME/Library/Logs"        "логи приложений"
  add 1 "Система и мусор" "$AS/com.apple.wallpaper"  "видео-обои, перекачаются"
  add 0 "Система и мусор" "$HOME/.Trash"             "уже в Корзине — просто очистите её"
  add 0 "Система и мусор" "$AS/MobileSync/Backup"    "бэкапы iPhone/iPad"
  add 0 "Система и мусор" "$AS/Google/DriveFS"       "кэш Google Drive — чистить в приложении"
  add 0 "Система и мусор" "$HOME/Library/Mail"       "локальная почта"
  add 0 "Система и мусор" "$HOME/Library/Messages"   "история iMessage"
  add 0 "Система и мусор" "$HOME/Downloads"          "загрузки — посмотрите глазами"
  add 0 "Система и мусор" "$AS/AnkiProgramFiles"     ""
  while IFS= read -r p; do
    add 0 "Система и мусор" "$p" "медиатека"
  done < <(find "$HOME/Pictures" -maxdepth 1 -name "*.photoslibrary" 2>/dev/null)

  # ---- 10. Возможно осиротевшее в Application Support ----
  while IFS= read -r p; do
    base=$(basename "$p")
    case "$base" in
      Apple*|com.apple.*|CloudDocs|AddressBook|CallHistory*|Knowledge|FileProvider|\
      Dock|iCloud*|SyncServices|CrashReporter|Google|JetBrains|Steam|Claude|Cursor|\
      Notion|Postman|Lens|Jan|CrossOver|Battle.net|MobileSync) continue ;;
    esac
    size=$(kb "$p"); [ "${size:-0}" -lt 204800 ] && continue   # только крупнее 200 МБ
    hit=""
    case "$base" in
      *.*.*) hit=$(mdfind "kMDItemCFBundleIdentifier == '$base'" 2>/dev/null | grep -m1 "\.app$") ;;
      *)     app_installed "$base" && hit="есть" ;;
    esac
    [ -z "$hit" ] && add 0 "Возможно осиротевшее" "$p" "приложение на диске не найдено — проверьте перед удалением"
  done < <(find "$AS" -maxdepth 1 -mindepth 1 -type d 2>/dev/null)

  printf "\r%-72s\r" "" >&2
}

if [ ! -s "$MANIFEST" ] || [ "$RESCAN" -eq 1 ] || \
   [ -n "$(find "$MANIFEST" -mmin +120 2>/dev/null)" ]; then
  scan
else
  echo "${d}Использую кэш от $(stat -f '%Sm' "$MANIFEST"). Пересчитать: --rescan${n}"
fi

# ======================= ОТЧЁТ =======================
free_before=$(df -k /System/Volumes/Data 2>/dev/null | awk 'NR==2{print $4}')
free_before=${free_before:-0}
echo
printf "${b}Свободно сейчас: %s${n}\n" "$(human "$free_before")"

GRP_LIST="Игры и Windows|Локальные модели|Мобильная разработка|JetBrains|Браузеры и Electron|Пакеты и тулчейны|Docker|Проекты|Система и мусор|Возможно осиротевшее"

grand_safe=0
IFS='|'; for grp in $GRP_LIST; do IFS=$' \t\n'
  gs=$(awk -F'\t' -v G="$grp" '$2==G && $1==1 {s+=$3} END{print s+0}' "$MANIFEST")
  gr=$(awk -F'\t' -v G="$grp" '$2==G && $1==0 {s+=$3} END{print s+0}' "$MANIFEST")
  [ "$gs" -eq 0 ] && [ "$gr" -eq 0 ] && continue
  echo
  printf "${b}${c}%s${n}  ${g}%s под нож${n}" "$grp" "$(human "$gs")"
  [ "$gr" -gt 0 ] && printf "  ${d}/ %s на ваше решение${n}" "$(human "$gr")"
  echo
  awk -F'\t' -v G="$grp" '$2==G' "$MANIFEST" | sort -t$'\t' -k3,3rn | head -18 | \
  while IFS=$'\t' read -r safe grp2 size path note; do
    mark="${g}✓${n}"; [ "$safe" = "0" ] && mark="${y}?${n}"
    printf "  %s %9s  %s\n" "$mark" "$(human "$size")" "$(tilde "$path")"
    [ -n "$note" ] && printf "    %9s  ${d}%s${n}\n" "" "$note"
  done
  extra=$(awk -F'\t' -v G="$grp" '$2==G' "$MANIFEST" | wc -l | tr -d ' ')
  [ "$extra" -gt 18 ] && printf "    ${d}… и ещё %d пунктов мельче${n}\n" "$((extra-18))"
  grand_safe=$((grand_safe+gs))
done
IFS=$' \t\n'

grand_review=$(awk -F'\t' '$1==0 {s+=$3} END{print s+0}' "$MANIFEST")
echo
echo "${b}════════════════════════════════════════════${n}"
printf "  ${g}${b}%12s${n}  освободится сразу (${g}✓${n})\n" "$(human "$grand_safe")"
printf "  ${y}%12s${n}  ещё столько под вопросом (${y}?${n})\n" "$(human "$grand_review")"
printf "  ${b}%12s${n}  свободно после первого этапа\n" "$(human "$((free_before+grand_safe))")"
echo "${b}════════════════════════════════════════════${n}"

if [ "$CLEAN" -eq 0 ]; then
  cat <<EOF

${d}Ничего не изменено. Дальше:${n}
  $0 --clean     перенести всё с ${g}✓${n} в Корзину (спросит по каждой группе)

${b}Отдельно, руками — скрипт за вас не сделает:${n}
  brew cleanup -s --prune=all           старые версии формул
  docker system prune -a --volumes      образы и тома, потом «Resize» в Docker Desktop
  xcrun simctl delete unavailable       мёртвые симуляторы
  tmutil listlocalsnapshots /           локальные снапшоты Time Machine, удалять
                                        через tmutil deletelocalsnapshots <дата>
  Google Drive → Настройки → Очистить кэш
  Chrome → chrome://settings/content/all → удалить данные ненужных сайтов
  sudo du -sh /Library/Caches/*         системные кэши, нужен пароль
EOF
  exit 0
fi

# ======================= ОЧИСТКА =======================
TRASH="$HOME/.Trash/deep-clean-$(date +%Y%m%d-%H%M%S)"
echo
echo "${b}Очистка${n} → $(tilde "$TRASH")"
echo "${d}Всё переносится, ничего не удаляется безвозвратно.${n}"

RUNNING=""
for p in Steam Claude Cursor Notion Postman Lens Jan "Google Chrome" Slack Docker \
         "Docker Desktop" Xcode ollama "LM Studio" VoiceInk CrossOver Whisky idea pycharm; do
  proc_running "$p" && RUNNING="$RUNNING|$p"
done
if [ -n "$RUNNING" ]; then
  echo
  echo "${r}${b}Запущены:${n}${RUNNING//|/ }"
  echo "${y}Закройте их и перезапустите — иначе часть папок вернётся при выходе приложения.${n}"
  printf "Продолжить всё равно? [y/N] "
  read -r ok </dev/tty
  case "$ok" in y|Y) ;; *) echo "Отменено."; exit 0 ;; esac
fi

mkdir -p "$TRASH"
moved=0
ALL=0

IFS='|'; for grp in $GRP_LIST; do IFS=$' \t\n'
  gs=$(awk -F'\t' -v G="$grp" '$2==G && $1==1 {s+=$3} END{print s+0}' "$MANIFEST")
  [ "$gs" -eq 0 ] && continue
  echo
  printf "${c}${b}%s${n} — %s\n" "$grp" "$(human "$gs")"
  if [ "$ALL" -eq 0 ]; then
    printf "  [y] группу целиком  [i] по одному  [s] пропустить  [A] всё остальное без вопросов: "
    read -r ans </dev/tty
  else
    ans=y
  fi
  case "$ans" in
    A) ALL=1; ans=y ;;
    s|S|"") echo "  ${d}пропущено${n}"; continue ;;
  esac

  awk -F'\t' -v G="$grp" '$2==G && $1==1' "$MANIFEST" | sort -t$'\t' -k3,3rn | \
  while IFS=$'\t' read -r safe grp2 size path note; do
    [ -e "$path" ] || continue
    if [ "$ans" = "i" ] || [ "$ans" = "I" ]; then
      printf "    %s — %s ? [y/N] " "$(tilde "$path")" "$(human "$size")"
      read -r one </dev/tty
      case "$one" in y|Y) ;; *) continue ;; esac
    fi
    rel="${path#$HOME_DIR/}"
    dest="$TRASH/$(echo "$rel" | tr '/' '_')"
    if mv "$path" "$dest" 2>/dev/null; then
      printf "    ${g}✓${n} %9s  %s\n" "$(human "$size")" "$(tilde "$path")"
      echo "$size" >> "$TRASH/.moved"
    else
      printf "    ${r}✗${n} %9s  %s ${d}(занято или нет прав)${n}\n" "$(human "$size")" "$(tilde "$path")"
    fi
  done
done
IFS=$' \t\n'

moved=$(awk '{s+=$1} END{print s+0}' "$TRASH/.moved" 2>/dev/null)
rm -f "$TRASH/.moved" 2>/dev/null
rmdir "$TRASH" 2>/dev/null
rm -f "$MANIFEST"

echo
printf "${g}${b}Перенесено в Корзину: %s${n}\n" "$(human "${moved:-0}")"
echo "${d}Проверьте, что нужные приложения запускаются, и очистите Корзину.${n}"
echo "${d}Место освободится только после очистки Корзины.${n}"
echo
