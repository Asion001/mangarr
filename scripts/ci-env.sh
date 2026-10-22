# Sourced by the CI jobs (.gitea/workflows/ci.yml): the values every
# job derives from the run.
#
#   BUILD         the build number stamped into binaries and images; workers
#                 compare it to decide whether to update (internal/workerupdate),
#                 so it continues past the old CI's numbers (it ended near 250)
#   INTERNAL_URL  the Gitea address the runner talks to, for uploads
#   API           the repository's API root there
#   IMAGE         the image as people pull it
if [ -n "${RUN:-}" ]; then
  export BUILD=$((RUN + 1000))
fi
INTERNAL_URL=${ACTIONS_RUNTIME_URL:-$GITHUB_SERVER_URL}
INTERNAL_URL=${INTERNAL_URL%%/api/actions_pipeline*}
export INTERNAL_URL=${INTERNAL_URL%/}
export API=$INTERNAL_URL/api/v1/repos/$GITHUB_REPOSITORY
host=${GITHUB_SERVER_URL#*://}
export IMAGE=${host%/}/$(printf '%s' "$GITHUB_REPOSITORY" | tr '[:upper:]' '[:lower:]')
