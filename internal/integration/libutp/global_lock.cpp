// See global_lock.h for why this exists.

#include "global_lock.h"

#include <pthread.h>

static pthread_mutex_t g_libutp_mu;
static pthread_once_t g_libutp_once = PTHREAD_ONCE_INIT;

static void libutp_global_lock_init(void) {
	pthread_mutexattr_t attr;
	pthread_mutexattr_init(&attr);
	// Recursive: libutp calls the embedder back from inside its own calls,
	// and the bridge answers some of those callbacks by calling libutp again
	// (cb_on_state_change -> pump_writes -> utp_write).
	pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_RECURSIVE);
	pthread_mutex_init(&g_libutp_mu, &attr);
	pthread_mutexattr_destroy(&attr);
}

extern "C" void libutp_global_lock(void) {
	pthread_once(&g_libutp_once, libutp_global_lock_init);
	pthread_mutex_lock(&g_libutp_mu);
}

extern "C" void libutp_global_unlock(void) {
	pthread_mutex_unlock(&g_libutp_mu);
}
